from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta

import pytest

from mobility_ml.field_features import (
    extract_field_feature_record,
    validate_field_feature_schema,
    verify_field_feature_hash,
)
from mobility_ml.generate_r07_dataset import generate_dataset
from mobility_ml.manifest import DatasetValidationError, dataset_sha256, find_contract_schema
from mobility_ml.rules_baseline import predict_rule


def holdout() -> dict:
    schema = find_contract_schema(filename="quality-field-holdout.v1.schema.json")
    return json.loads(
        (schema.parent.parent / "fixtures/quality-field-holdout.v1.valid.json").read_text()
    )


def linked_inputs() -> tuple[dict, dict]:
    manifest = holdout()
    batch = generate_dataset()["traces"][0]["batch"]
    original_start = datetime.fromisoformat(
        batch["samples"][0]["capturedAt"].replace("Z", "+00:00")
    )
    field_start = datetime(2026, 9, 1, 1, 0, tzinfo=UTC)
    for sample in batch["samples"]:
        captured = datetime.fromisoformat(sample["capturedAt"].replace("Z", "+00:00"))
        sample["capturedAt"] = (
            (field_start + (captured - original_start)).isoformat().replace("+00:00", "Z")
        )
    batch["sentAt"] = (field_start + timedelta(minutes=10)).isoformat().replace("+00:00", "Z")
    entry = manifest["traces"][0]
    entry["telemetryBatchId"] = batch["clientBatchId"]
    entry["telemetrySha256"] = dataset_sha256(batch)
    entry["capturedAt"] = batch["samples"][0]["capturedAt"]
    entry["sampleCount"] = len(batch["samples"])
    return batch, manifest


def test_field_feature_is_coordinate_free_and_hash_verified() -> None:
    batch, manifest = linked_inputs()
    record = extract_field_feature_record(
        batch, manifest, trace_id=manifest["traces"][0]["traceId"]
    )
    assert record["extractionStatus"] == "ok"
    assert verify_field_feature_hash(record)
    assert not {"latitude", "longitude", "pseudonymousGroupId", "expectedLabel"}.intersection(
        record
    )
    assert record["holdoutManifestSha256"] == dataset_sha256(manifest)


def test_batch_identity_and_telemetry_hash_mismatch_are_rejected() -> None:
    batch, manifest = linked_inputs()
    batch["clientBatchId"] = "123e4567-e89b-42d3-a456-426614174099"
    with pytest.raises(DatasetValidationError, match="batch identity:mismatch"):
        extract_field_feature_record(batch, manifest, trace_id=manifest["traces"][0]["traceId"])

    batch, manifest = linked_inputs()
    manifest["traces"][0]["telemetrySha256"] = "1" * 64
    with pytest.raises(DatasetValidationError, match="telemetrySha256:mismatch"):
        extract_field_feature_record(batch, manifest, trace_id=manifest["traces"][0]["traceId"])


def test_fixture_has_no_coordinate_or_identity_escape_hatch() -> None:
    schema = find_contract_schema(filename="quality-field-features.v1.schema.json")
    fixture = json.loads(
        (schema.parent.parent / "fixtures/quality-field-features.v1.valid.json").read_text()
    )
    assert not {"latitude", "longitude", "pseudonymousGroupId", "consentProofDigest"}.intersection(
        fixture
    )
    assert not verify_field_feature_hash(fixture)
    with pytest.raises(DatasetValidationError, match="featureSha256:mismatch"):
        validate_field_feature_schema(fixture)


def test_field_numeric_feature_contract_tracks_synthetic_extractor_contract() -> None:
    field_schema = find_contract_schema(filename="quality-field-features.v1.schema.json")
    synthetic_schema = find_contract_schema(filename="quality-features.v1.schema.json")
    field_document = json.loads(field_schema.read_text())
    synthetic_document = json.loads(synthetic_schema.read_text())
    assert (
        field_document["$defs"]["numericFeatures"] == synthetic_document["$defs"]["numericFeatures"]
    )


def test_rules_baseline_accepts_the_same_verified_field_feature_boundary() -> None:
    batch, manifest = linked_inputs()
    record = extract_field_feature_record(
        batch, manifest, trace_id=manifest["traces"][0]["traceId"]
    )
    prediction = predict_rule(record)
    assert prediction["status"] in {"predicted", "abstain"}
    assert prediction["featureHash"] == record["featureSha256"]
    assert not {"expectedLabel", "pseudonymousGroupId", "latitude", "longitude"}.intersection(
        prediction
    )
