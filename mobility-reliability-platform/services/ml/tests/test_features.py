from __future__ import annotations

import copy
import inspect
import json
from pathlib import Path
from typing import Any

import pytest

from mobility_ml.features import (
    FEATURE_EXTRACTOR_VERSION,
    FEATURE_SCHEMA_VERSION,
    FEATURE_STATUS_READY,
    FEATURE_STATUS_REVIEW,
    calculate_feature_core,
    extract_feature_record,
    validate_feature_record_schema,
    verify_feature_hash,
)
from mobility_ml.manifest import DatasetValidationError

FIXTURE_PATH = Path(__file__).parent / "fixtures" / "r07_feature_golden.json"


def _golden() -> dict[str, Any]:
    return json.loads(FIXTURE_PATH.read_text(encoding="utf-8"))


def _record_from_golden() -> dict[str, Any]:
    golden = _golden()
    return extract_feature_record(
        golden["batch"],
        trace_id=golden["lineage"]["traceId"],
        dataset_hash=golden["lineage"]["datasetSha256"],
        split=golden["lineage"]["split"],
    )


def _keys(value: Any) -> set[str]:
    if isinstance(value, dict):
        return set(value) | {key for child in value.values() for key in _keys(child)}
    if isinstance(value, list):
        return {key for child in value for key in _keys(child)}
    return set()


def test_feature_core_boundary_has_no_label_or_split_inputs() -> None:
    parameters = inspect.signature(calculate_feature_core).parameters
    assert tuple(parameters) == ("batch",)


def test_golden_feature_vector_is_deterministic() -> None:
    golden = _golden()
    first = _record_from_golden()
    second = _record_from_golden()
    assert first == second
    assert first["schemaVersion"] == FEATURE_SCHEMA_VERSION
    assert first["lineage"]["feature"]["extractorVersion"] == FEATURE_EXTRACTOR_VERSION
    assert (
        first["extractionStatus"] == golden["expected"]["extractionStatus"] == FEATURE_STATUS_READY
    )
    assert first["reasonCode"] == golden["expected"]["reasonCode"]
    assert first["features"] == golden["expected"]["features"]
    assert verify_feature_hash(first)


def test_feature_record_schema_and_coordinate_free_output() -> None:
    record = _record_from_golden()
    validate_feature_record_schema(record)
    assert not {"latitude", "longitude", "samples", "rawCoordinates"}.intersection(_keys(record))


@pytest.mark.parametrize(
    ("field", "value", "expected_reason"),
    [
        ("latitude", float("nan"), "non_finite_value"),
        ("horizontalAccuracyM", -1, "contract_mismatch"),
        ("sequence", 9, "extractor_error"),
        ("capturedAt", "2026-08-10T23:59:59Z", "non_monotonic_time"),
    ],
)
def test_malformed_sample_is_reviewed_without_raw_values(
    field: str, value: Any, expected_reason: str
) -> None:
    golden = _golden()
    batch = copy.deepcopy(golden["batch"])
    batch["samples"][1][field] = value
    record = extract_feature_record(
        batch,
        trace_id=golden["lineage"]["traceId"],
        dataset_hash=golden["lineage"]["datasetSha256"],
        split=golden["lineage"]["split"],
    )
    assert record["extractionStatus"] == FEATURE_STATUS_REVIEW
    assert record["reasonCode"] == expected_reason
    assert record["features"] is None
    assert verify_feature_hash(record)
    validate_feature_record_schema(record)
    assert not {"latitude", "longitude", "samples"}.intersection(_keys(record))


def test_label_or_split_added_to_batch_is_contract_mismatch() -> None:
    golden = _golden()
    batch = copy.deepcopy(golden["batch"])
    batch["label"] = "vehicle_likely"
    batch["split"] = "test"
    record = extract_feature_record(
        batch,
        trace_id=golden["lineage"]["traceId"],
        dataset_hash=golden["lineage"]["datasetSha256"],
        split=golden["lineage"]["split"],
    )
    assert record["extractionStatus"] == FEATURE_STATUS_REVIEW
    assert record["reasonCode"] == "contract_mismatch"
    validate_feature_record_schema(record)


def test_feature_hash_detects_numeric_tampering() -> None:
    record = _record_from_golden()
    tampered = copy.deepcopy(record)
    tampered["features"]["pathLengthM"] += 1
    assert not verify_feature_hash(tampered)


def test_feature_hash_detects_lineage_tampering() -> None:
    record = _record_from_golden()
    tampered = copy.deepcopy(record)
    tampered["lineage"]["dataset"]["split"] = "test"
    assert not verify_feature_hash(tampered)


@pytest.mark.parametrize(
    ("field", "value", "message"),
    [
        ("trace_id", "not-a-uuid", "trace identity invalid"),
        ("dataset_hash", "not-a-sha", "dataset hash invalid"),
        ("split", "future", "split invalid"),
    ],
)
def test_invalid_record_identity_fails_closed(field: str, value: str, message: str) -> None:
    golden = _golden()
    arguments = {
        "trace_id": golden["lineage"]["traceId"],
        "dataset_hash": golden["lineage"]["datasetSha256"],
        "split": golden["lineage"]["split"],
    }
    arguments[field] = value
    with pytest.raises(DatasetValidationError, match=message):
        extract_feature_record(golden["batch"], **arguments)


def test_non_synthetic_source_is_reviewed_without_features() -> None:
    golden = _golden()
    record = extract_feature_record(
        golden["batch"],
        trace_id=golden["lineage"]["traceId"],
        dataset_hash=golden["lineage"]["datasetSha256"],
        split=golden["lineage"]["split"],
        source_kind="field_pilot",
        benchmark_eligible=False,
    )
    assert record["extractionStatus"] == FEATURE_STATUS_REVIEW
    assert record["reasonCode"] == "unsupported_source"
    assert record["features"] is None
    assert verify_feature_hash(record)
    validate_feature_record_schema(record)


def test_all_missing_accuracy_requires_review() -> None:
    golden = _golden()
    batch = copy.deepcopy(golden["batch"])
    for sample in batch["samples"]:
        sample["horizontalAccuracyM"] = None
    record = extract_feature_record(
        batch,
        trace_id=golden["lineage"]["traceId"],
        dataset_hash=golden["lineage"]["datasetSha256"],
        split=golden["lineage"]["split"],
    )
    assert record["extractionStatus"] == FEATURE_STATUS_REVIEW
    assert record["reasonCode"] == "invalid_or_missing_accuracy"
    assert record["features"] is None
    validate_feature_record_schema(record)


def test_missing_batch_identity_fails_closed_without_payload() -> None:
    golden = _golden()
    batch = copy.deepcopy(golden["batch"])
    del batch["clientBatchId"]
    with pytest.raises(DatasetValidationError, match="batch identity invalid") as raised:
        extract_feature_record(
            batch,
            trace_id=golden["lineage"]["traceId"],
            dataset_hash=golden["lineage"]["datasetSha256"],
            split=golden["lineage"]["split"],
        )
    assert "latitude" not in str(raised.value)
