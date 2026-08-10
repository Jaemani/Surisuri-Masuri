from __future__ import annotations

import copy
from typing import Any

import pytest

from mobility_ml.features import FeatureLeakageError, extract_dataset_features
from mobility_ml.generate_r07_dataset import build_manifest, generate_dataset
from mobility_ml.manifest import DatasetValidationError
from mobility_ml.rules_baseline import (
    evaluate_frozen_dataset,
    predict_rule,
    validate_baseline_result_schema,
)


def _frozen() -> tuple[dict[str, Any], dict[str, Any], dict[str, dict[str, Any]]]:
    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    records = extract_dataset_features(dataset, manifest)
    return dataset, manifest, records


def test_rules_evaluation_is_deterministic_and_synthetic_only() -> None:
    dataset, manifest, records = _frozen()
    first = evaluate_frozen_dataset(dataset, manifest, records)
    second = evaluate_frozen_dataset(dataset, manifest, records)
    assert first == second
    assert first["evaluationScope"] == "synthetic_only"
    assert first["sourceKind"] == "synthetic"
    assert first["benchmarkEligible"] is True
    assert first["overall"]["count"] == 48
    assert first["overall"]["abstainCount"] == 0
    assert first["overall"]["macroF1"] == 1.0
    assert set(first["splits"]) == {"train", "validation", "test"}
    assert all(metrics["count"] == 16 for metrics in first["splits"].values())
    assert all("latitude" not in prediction for prediction in first["predictions"])
    validate_baseline_result_schema(first)


def test_baseline_result_schema_rejects_missing_synthetic_provenance() -> None:
    dataset, manifest, records = _frozen()
    result = evaluate_frozen_dataset(dataset, manifest, records)
    del result["sourceKind"]
    with pytest.raises(DatasetValidationError, match="quality-baseline-result.v1 invalid"):
        validate_baseline_result_schema(result)


def test_rule_prediction_returns_verified_feature_hash() -> None:
    _, _, records = _frozen()
    prediction = predict_rule(next(iter(records.values())))
    assert isinstance(prediction["featureHash"], str)
    assert len(prediction["featureHash"]) == 64


def test_feature_label_leakage_is_rejected_at_prediction_boundary() -> None:
    _, _, records = _frozen()
    record = copy.deepcopy(next(iter(records.values())))
    record["label"] = "vehicle_likely"
    with pytest.raises(FeatureLeakageError):
        predict_rule(record)


def test_tampered_feature_hash_is_rejected() -> None:
    _, _, records = _frozen()
    record = copy.deepcopy(next(iter(records.values())))
    record["features"]["sampleCount"] += 1
    with pytest.raises(ValueError, match="feature hash mismatch"):
        predict_rule(record)


def test_tampered_feature_lineage_is_rejected() -> None:
    _, _, records = _frozen()
    record = copy.deepcopy(next(iter(records.values())))
    record["lineage"]["telemetryBatch"]["batchSha256"] = "0" * 64
    with pytest.raises(ValueError, match="feature hash mismatch"):
        predict_rule(record)


def test_tampered_manifest_is_rejected_before_evaluation() -> None:
    dataset, manifest, records = _frozen()
    manifest["datasetSha256"] = "0" * 64
    with pytest.raises(DatasetValidationError):
        evaluate_frozen_dataset(dataset, manifest, records)
