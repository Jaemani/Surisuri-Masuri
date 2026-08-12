from __future__ import annotations

import copy
import hashlib
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest

import mobility_ml.torch_candidate as torch_candidate
from mobility_ml.field_evaluation import evaluate_field_holdout, validate_field_evaluation_result
from mobility_ml.field_features import extract_field_feature_record
from mobility_ml.generate_r07_dataset import build_manifest, generate_dataset
from mobility_ml.manifest import (
    DatasetValidationError,
    canonical_json,
    dataset_sha256,
    find_contract_schema,
)
from mobility_ml.torch_candidate import export_frozen_artifact, load_frozen_artifact


def _field_inputs(tmp_path: Path) -> tuple[dict, dict[str, dict], object]:
    dataset = generate_dataset()
    synthetic_manifest = build_manifest(dataset)
    artifact_path = tmp_path / "artifact"
    export_frozen_artifact(
        dataset,
        synthetic_manifest,
        output_path=artifact_path,
        epochs=120,
    )
    artifact = load_frozen_artifact(artifact_path)
    schema = find_contract_schema(filename="quality-field-holdout.v1.schema.json")
    manifest = json.loads(
        (schema.parent.parent / "fixtures/quality-field-holdout.v1.valid.json").read_text()
    )
    manifest["trainingBoundary"]["frozenModelStateSha256"] = artifact.metadata["modelStateSha256"]
    batch = copy.deepcopy(dataset["traces"][0]["batch"])
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
    feature = extract_field_feature_record(batch, manifest, trace_id=entry["traceId"])
    return manifest, {entry["traceId"]: feature}, artifact


def test_field_evaluation_uses_same_cohort_without_training(tmp_path: Path, monkeypatch) -> None:
    manifest, features, artifact = _field_inputs(tmp_path)

    def forbidden_training(*_args, **_kwargs):
        raise AssertionError("field evaluation called training")

    monkeypatch.setattr(torch_candidate, "_fit_candidate", forbidden_training)
    result = evaluate_field_holdout(
        manifest,
        features,
        artifact,
        evaluated_at="2026-09-10T10:00:00Z",
    )
    assert result["trainingPerformed"] is False
    assert result["deploymentAuthorized"] is False
    assert result["deploymentDecision"] == "defer"
    assert result["cohort"]["scoredTraceCount"] == 1
    assert result["rulesMetrics"]["count"] == result["modelMetrics"]["count"] == 1
    assert len(result["predictions"]) == 1
    assert not {
        "pseudonymousGroupId",
        "consentProofDigest",
        "latitude",
        "longitude",
        "features",
    }.intersection(result["predictions"][0])


def test_field_evaluation_rejects_model_or_feature_lineage_mismatch(tmp_path: Path) -> None:
    manifest, features, artifact = _field_inputs(tmp_path)
    wrong_model = copy.deepcopy(manifest)
    wrong_model["trainingBoundary"]["frozenModelStateSha256"] = "1" * 64
    with pytest.raises(DatasetValidationError, match="frozen model state:mismatch"):
        evaluate_field_holdout(wrong_model, features, artifact, evaluated_at="2026-09-10T10:00:00Z")

    wrong_feature = copy.deepcopy(features)
    trace_id = manifest["traces"][0]["traceId"]
    wrong_feature[trace_id]["holdoutManifestSha256"] = "2" * 64
    with pytest.raises(DatasetValidationError, match="featureSha256:mismatch"):
        evaluate_field_holdout(
            manifest, wrong_feature, artifact, evaluated_at="2026-09-10T10:00:00Z"
        )


def test_field_evaluation_window_is_fail_closed(tmp_path: Path) -> None:
    manifest, features, artifact = _field_inputs(tmp_path)
    with pytest.raises(DatasetValidationError, match="outside_window"):
        evaluate_field_holdout(manifest, features, artifact, evaluated_at="2027-01-01T00:00:00Z")


def test_field_evaluation_count_reconciliation_is_semantic(tmp_path: Path) -> None:
    manifest, features, artifact = _field_inputs(tmp_path)
    result = evaluate_field_holdout(
        manifest, features, artifact, evaluated_at="2026-09-10T10:00:00Z"
    )
    tampered = copy.deepcopy(result)
    tampered["cohort"]["scoredTraceCount"] = 0
    payload = {key: value for key, value in tampered.items() if key != "evaluationResultSha256"}
    tampered["evaluationResultSha256"] = hashlib.sha256(canonical_json(payload)).hexdigest()
    with pytest.raises(DatasetValidationError, match="cohort:scored_count"):
        validate_field_evaluation_result(tampered)
