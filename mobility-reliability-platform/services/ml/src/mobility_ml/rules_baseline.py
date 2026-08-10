"""Deterministic, synthetic-only rules baseline and split evaluator."""

from __future__ import annotations

import json
import math
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Any

from .features import (
    FEATURE_SCHEMA_VERSION,
    FEATURE_STATUS_READY,
    FeatureLeakageError,
    extract_dataset_features,
    find_contract_schema,
    validate_feature_record_schema,
    verify_feature_hash,
)
from .manifest import (
    ABSTAIN_LABEL,
    DATASET_VERSION,
    KNOWN_LABELS,
    SPLIT_STRATEGY,
    DatasetValidationError,
    validate_manifest_against_dataset,
)

RULE_BASELINE_VERSION = "r07-rules-baseline.v1"
METRICS_VERSION = "r07-synthetic-metrics.v1"
EVALUATION_SCOPE = "synthetic_only"
BASELINE_RESULT_SCHEMA_FILENAME = "quality-baseline-result.v1.schema.json"

GPS_NOISE_ACCURACY_MEAN_M = 60.0
GPS_NOISE_ACCURACY_MAX_M = 100.0
STATIONARY_RATIO_THRESHOLD = 0.75
STATIONARY_SPEED_MPS = 0.3
VEHICLE_SPEED_MPS = 4.0
MIN_SAMPLES = 3
MAX_MISSING_RATIO = 0.6


def validate_baseline_result_schema(
    result: Mapping[str, Any], schema_path: Path | str | None = None
) -> None:
    """Validate a synthetic baseline result against the repository contract."""

    try:
        schema = (
            Path(schema_path).expanduser().resolve()
            if schema_path is not None
            else find_contract_schema(filename=BASELINE_RESULT_SCHEMA_FILENAME)
        )
        schema_document = json.loads(schema.read_text(encoding="utf-8"))
    except (FileNotFoundError, OSError, json.JSONDecodeError) as error:
        raise DatasetValidationError("quality-baseline-result.v1 schema unavailable") from error
    try:
        import jsonschema  # type: ignore[import-not-found]
    except ImportError as error:
        raise DatasetValidationError("quality-baseline-result.v1 validator unavailable") from error
    errors = list(
        jsonschema.Draft202012Validator(
            schema_document, format_checker=jsonschema.FormatChecker()
        ).iter_errors(result)
    )
    if errors:
        paths = ", ".join(
            ".".join(str(part) for part in error.absolute_path) or "$" for error in errors[:12]
        )
        raise DatasetValidationError(f"quality-baseline-result.v1 invalid: {paths}")


def _round(value: float) -> float:
    return round(float(value), 12)


def _feature_input(record: Mapping[str, Any]) -> Mapping[str, Any]:
    """Reject ground-truth metadata at the prediction boundary."""

    forbidden = {"label", "split", "expectedLabel", "scenarioGroupId"}
    if forbidden.intersection(record):
        raise FeatureLeakageError("label or split metadata entered rules feature boundary")
    if record.get("extractionStatus") != FEATURE_STATUS_READY:
        return {}
    features = record.get("features")
    if not isinstance(features, Mapping):
        return {}
    if forbidden.intersection(features):
        raise FeatureLeakageError("label or split metadata entered rules feature vector")
    return features


def _feature_hash(record: Mapping[str, Any]) -> str | None:
    lineage = record.get("lineage")
    if not isinstance(lineage, Mapping):
        return None
    feature_lineage = lineage.get("feature")
    if not isinstance(feature_lineage, Mapping):
        return None
    value = feature_lineage.get("featureSha256")
    return value if isinstance(value, str) else None


def predict_rule(feature_record: Mapping[str, Any]) -> dict[str, Any]:
    """Return one prediction without accessing labels or split metadata."""

    features = _feature_input(feature_record)
    if not verify_feature_hash(feature_record):
        raise ValueError("feature hash mismatch")
    if feature_record.get("extractionStatus") != FEATURE_STATUS_READY or not features:
        return {
            "ruleVersion": RULE_BASELINE_VERSION,
            "status": "abstain",
            "predictedLabel": ABSTAIN_LABEL,
            "abstain": True,
            "reasonCode": "feature_review_required",
            "featureHash": _feature_hash(feature_record),
        }
    sample_count = int(features.get("sampleCount", 0))
    missing_ratio = float(features.get("optionalFieldMissingRatio", 1.0))
    accuracy_mean = float(features.get("accuracyMeanM", math.inf))
    accuracy_max = float(features.get("accuracyMaxM", math.inf))
    if sample_count < MIN_SAMPLES:
        label, reason = ABSTAIN_LABEL, "insufficient_samples"
    elif accuracy_mean >= GPS_NOISE_ACCURACY_MEAN_M or accuracy_max >= GPS_NOISE_ACCURACY_MAX_M:
        label, reason = "gps_noise_or_insufficient", "accuracy_high"
    elif missing_ratio > MAX_MISSING_RATIO:
        label, reason = ABSTAIN_LABEL, "missingness_high"
    elif (
        float(features.get("stationaryRatio", 0.0)) >= STATIONARY_RATIO_THRESHOLD
        and float(features.get("derivedSpeedMeanMps", 0.0)) <= STATIONARY_SPEED_MPS
    ):
        label, reason = "stationary", "stationary_ratio"
    elif (
        float(features.get("reportedSpeedMeanMps", 0.0)) >= VEHICLE_SPEED_MPS
        or float(features.get("derivedSpeedMeanMps", 0.0)) >= VEHICLE_SPEED_MPS
    ):
        label, reason = "vehicle_likely", "speed_high"
    elif (
        float(features.get("reportedSpeedMeanMps", 0.0)) > STATIONARY_SPEED_MPS
        or float(features.get("derivedSpeedMeanMps", 0.0)) > STATIONARY_SPEED_MPS
    ):
        label, reason = "mobility_aid_likely", "wheeled_speed_band"
    else:
        label, reason = ABSTAIN_LABEL, "movement_signal_insufficient"
    return {
        "ruleVersion": RULE_BASELINE_VERSION,
        "status": "abstain" if label == ABSTAIN_LABEL else "predicted",
        "predictedLabel": label,
        "abstain": label == ABSTAIN_LABEL,
        "reasonCode": reason,
        "featureHash": _feature_hash(feature_record),
    }


def _empty_matrix() -> dict[str, dict[str, int]]:
    columns = (*KNOWN_LABELS, ABSTAIN_LABEL)
    return {actual: {predicted: 0 for predicted in columns} for actual in columns}


def _metrics(rows: Iterable[Mapping[str, Any]]) -> dict[str, Any]:
    rows = list(rows)
    matrix = _empty_matrix()
    for row in rows:
        actual = row["expectedLabel"]
        predicted = row["predictedLabel"]
        matrix[actual][predicted] += 1
    precision: dict[str, float] = {}
    recall: dict[str, float] = {}
    f1: dict[str, float] = {}
    for label in KNOWN_LABELS:
        true_positive = matrix[label][label]
        false_positive = (
            sum(matrix[actual][label] for actual in (*KNOWN_LABELS, ABSTAIN_LABEL)) - true_positive
        )
        false_negative = (
            sum(matrix[label][predicted] for predicted in (*KNOWN_LABELS, ABSTAIN_LABEL))
            - true_positive
        )
        p = (
            true_positive / (true_positive + false_positive)
            if true_positive + false_positive
            else 0.0
        )
        r = (
            true_positive / (true_positive + false_negative)
            if true_positive + false_negative
            else 0.0
        )
        precision[label] = _round(p)
        recall[label] = _round(r)
        f1[label] = _round(2 * p * r / (p + r) if p + r else 0.0)
    total = len(rows)
    abstain_count = sum(row["abstain"] for row in rows)
    correct = sum(row["expectedLabel"] == row["predictedLabel"] for row in rows)
    return {
        "count": total,
        "abstainCount": abstain_count,
        "abstainRate": _round(abstain_count / total if total else 0.0),
        "coverage": _round((total - abstain_count) / total if total else 0.0),
        "accuracyIncludingAbstain": _round(correct / total if total else 0.0),
        "confusionMatrix": matrix,
        "precision": precision,
        "recall": recall,
        "f1": f1,
        "macroF1": _round(sum(f1.values()) / len(KNOWN_LABELS)),
    }


def evaluate_frozen_dataset(
    dataset: Mapping[str, Any],
    manifest: Mapping[str, Any],
    feature_records: Mapping[str, Mapping[str, Any]] | None = None,
) -> dict[str, Any]:
    """Evaluate rules on the frozen dataset without resplitting or relabeling."""

    validate_manifest_against_dataset(manifest, dataset)
    frozen_hash = manifest["datasetSha256"]
    if feature_records is None:
        feature_records = extract_dataset_features(dataset, manifest)
    expected_trace_ids = {trace["traceId"] for trace in dataset["traces"]}
    if set(feature_records) != expected_trace_ids:
        raise DatasetValidationError("feature trace lineage does not match frozen dataset")
    rows: list[dict[str, Any]] = []
    for trace in sorted(dataset["traces"], key=lambda item: item["traceId"]):
        trace_id = trace["traceId"]
        feature_record = feature_records[trace_id]
        if not isinstance(feature_record, Mapping) or not verify_feature_hash(feature_record):
            raise DatasetValidationError("feature hash or record invalid")
        validate_feature_record_schema(feature_record)
        lineage = feature_record.get("lineage")
        if not isinstance(lineage, Mapping):
            raise DatasetValidationError("feature lineage missing")
        trace_lineage = lineage.get("trace")
        dataset_lineage = lineage.get("dataset")
        if (
            not isinstance(trace_lineage, Mapping)
            or not isinstance(dataset_lineage, Mapping)
            or trace_lineage.get("traceId") != trace_id
            or dataset_lineage.get("datasetSha256") != frozen_hash
            or dataset_lineage.get("split") != trace["split"]
        ):
            raise DatasetValidationError("feature lineage mismatch")
        if {"label", "split", "expectedLabel"}.intersection(feature_record):
            raise FeatureLeakageError("label or split metadata entered evaluation feature record")
        prediction = predict_rule(feature_record)
        rows.append(
            {
                "traceId": trace_id,
                "split": trace["split"],
                "expectedLabel": trace["label"],
                "predictedLabel": prediction["predictedLabel"],
                "abstain": prediction["abstain"],
                "reasonCode": prediction["reasonCode"],
                "featureHash": _feature_hash(feature_record),
            }
        )
    splits = {
        split: _metrics(row for row in rows if row["split"] == split)
        for split in ("train", "validation", "test")
    }
    result = {
        "metricsVersion": METRICS_VERSION,
        "evaluationScope": EVALUATION_SCOPE,
        "sourceKind": "synthetic",
        "benchmarkEligible": True,
        "datasetVersion": DATASET_VERSION,
        "datasetSha256": frozen_hash,
        "featureSchemaVersion": FEATURE_SCHEMA_VERSION,
        "ruleVersion": RULE_BASELINE_VERSION,
        "splitStrategy": SPLIT_STRATEGY,
        "splits": splits,
        "overall": _metrics(rows),
        "predictions": rows,
    }
    validate_baseline_result_schema(result)
    return result
