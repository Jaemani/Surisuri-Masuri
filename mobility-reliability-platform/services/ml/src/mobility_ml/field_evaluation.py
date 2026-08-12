"""Evaluation-only comparison on an admitted field holdout; no training path exists here."""

from __future__ import annotations

import hashlib
from collections.abc import Mapping
from datetime import UTC, datetime
from typing import Any

from .field_features import validate_field_feature_schema
from .field_holdout import validate_field_holdout
from .manifest import (
    ABSTAIN_LABEL,
    KNOWN_LABELS,
    DatasetValidationError,
    _validate_repository_schema,
    canonical_json,
    dataset_sha256,
)
from .rules_baseline import RULE_BASELINE_VERSION, predict_rule
from .torch_candidate import FrozenInferenceArtifact, predict_frozen

FIELD_EVALUATION_VERSION = "quality-field-evaluation-result.v1"


def _timestamp(value: str) -> datetime:
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except (ValueError, AttributeError) as error:
        raise DatasetValidationError("field evaluation evaluatedAt:date-time") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise DatasetValidationError("field evaluation evaluatedAt:date-time")
    return parsed.astimezone(UTC)


def _metrics(rows: list[dict[str, str]]) -> dict[str, Any]:
    if not rows:
        return {"count": 0, "accuracy": None, "macroF1": None, "abstainCount": 0}
    correct = sum(row["expectedLabel"] == row["predictedLabel"] for row in rows)
    f1: list[float] = []
    for label in KNOWN_LABELS:
        true_positive = sum(
            row["expectedLabel"] == label and row["predictedLabel"] == label for row in rows
        )
        false_positive = sum(
            row["expectedLabel"] != label and row["predictedLabel"] == label for row in rows
        )
        false_negative = sum(
            row["expectedLabel"] == label and row["predictedLabel"] != label for row in rows
        )
        precision = (
            true_positive / (true_positive + false_positive)
            if true_positive + false_positive
            else 0
        )
        recall = (
            true_positive / (true_positive + false_negative)
            if true_positive + false_negative
            else 0
        )
        f1.append(2 * precision * recall / (precision + recall) if precision + recall else 0)
    return {
        "count": len(rows),
        "accuracy": round(correct / len(rows), 12),
        "macroF1": round(sum(f1) / len(f1), 12),
        "abstainCount": sum(row["predictedLabel"] == ABSTAIN_LABEL for row in rows),
    }


def validate_field_evaluation_result(result: Mapping[str, Any]) -> None:
    _validate_repository_schema(
        result,
        filename="quality-field-evaluation-result.v1.schema.json",
        error_type=DatasetValidationError,
        label=FIELD_EVALUATION_VERSION,
    )
    expected = result.get("evaluationResultSha256")
    payload = {key: value for key, value in result.items() if key != "evaluationResultSha256"}
    if expected != hashlib.sha256(canonical_json(payload)).hexdigest():
        raise DatasetValidationError("quality-field-evaluation-result.v1 hash:mismatch")
    cohort = result["cohort"]
    if cohort["labelEligibleCount"] + cohort["labelReviewCount"] != cohort["totalTraceCount"]:
        raise DatasetValidationError("quality-field-evaluation-result.v1 cohort:label_count")
    if (
        cohort["featureReadyCount"] + cohort["featureReviewCount"] + cohort["missingFeatureCount"]
        != cohort["labelEligibleCount"]
    ):
        raise DatasetValidationError("quality-field-evaluation-result.v1 cohort:feature_count")
    predictions = result["predictions"]
    if (
        cohort["scoredTraceCount"] != cohort["featureReadyCount"]
        or cohort["scoredTraceCount"] != len(predictions)
        or result["rulesMetrics"]["count"] != cohort["scoredTraceCount"]
        or result["modelMetrics"]["count"] != cohort["scoredTraceCount"]
    ):
        raise DatasetValidationError("quality-field-evaluation-result.v1 cohort:scored_count")
    trace_ids = [prediction["traceId"] for prediction in predictions]
    if len(trace_ids) != len(set(trace_ids)):
        raise DatasetValidationError("quality-field-evaluation-result.v1 predictions:duplicate")


def evaluate_field_holdout(
    manifest: Mapping[str, Any],
    features_by_trace: Mapping[str, Mapping[str, Any]],
    artifact: FrozenInferenceArtifact,
    *,
    evaluated_at: str,
) -> dict[str, Any]:
    """Compare frozen rules/model on the exact same eligible field trace set."""

    validate_field_holdout(manifest)
    evaluated = _timestamp(evaluated_at)
    retention = manifest["retention"]
    if (
        not _timestamp(retention["evaluationNotBeforeAt"])
        <= evaluated
        < _timestamp(retention["evaluationExpiresAt"])
    ):
        raise DatasetValidationError("field evaluation evaluatedAt:outside_window")
    metadata = artifact.metadata
    if metadata["modelStateSha256"] != manifest["trainingBoundary"]["frozenModelStateSha256"]:
        raise DatasetValidationError("field evaluation frozen model state:mismatch")
    if manifest["trainingBoundary"]["frozenRulesVersion"] != RULE_BASELINE_VERSION:
        raise DatasetValidationError("field evaluation frozen rules version:mismatch")
    manifest_hash = dataset_sha256(manifest)
    trace_ids = {entry["traceId"] for entry in manifest["traces"]}
    if set(features_by_trace) - trace_ids:
        raise DatasetValidationError("field evaluation feature trace:unexpected")

    predictions: list[dict[str, str]] = []
    rule_rows: list[dict[str, str]] = []
    model_rows: list[dict[str, str]] = []
    label_eligible = feature_ready = label_review = feature_review = missing = 0
    groups: set[str] = set()
    for entry in manifest["traces"]:
        groups.add(entry["pseudonymousGroupId"])
        if not entry["labelEligible"]:
            label_review += 1
            continue
        label_eligible += 1
        feature = features_by_trace.get(entry["traceId"])
        if feature is None:
            missing += 1
            continue
        validate_field_feature_schema(feature)
        if (
            feature["holdoutId"] != manifest["holdoutId"]
            or feature["holdoutManifestSha256"] != manifest_hash
            or feature["traceId"] != entry["traceId"]
            or feature["telemetryBatchId"] != entry["telemetryBatchId"]
            or feature["telemetrySha256"] != entry["telemetrySha256"]
        ):
            raise DatasetValidationError("field evaluation feature lineage:mismatch")
        if feature["extractionStatus"] != "ok":
            feature_review += 1
            continue
        feature_ready += 1
        rule = predict_rule(feature)
        model = predict_frozen(artifact, feature)
        expected = entry["expectedLabel"]
        prediction = {
            "traceId": entry["traceId"],
            "featureSha256": feature["featureSha256"],
            "expectedLabel": expected,
            "rulesPredictedLabel": rule["predictedLabel"],
            "modelPredictedLabel": model["predictedLabel"],
        }
        predictions.append(prediction)
        rule_rows.append({"expectedLabel": expected, "predictedLabel": rule["predictedLabel"]})
        model_rows.append({"expectedLabel": expected, "predictedLabel": model["predictedLabel"]})

    result: dict[str, Any] = {
        "schemaVersion": FIELD_EVALUATION_VERSION,
        "evaluationScope": "field_holdout_evaluation_only",
        "evaluatedAt": evaluated_at,
        "holdoutId": manifest["holdoutId"],
        "holdoutManifestSha256": manifest_hash,
        "modelVersion": metadata["modelVersion"],
        "modelStateSha256": metadata["modelStateSha256"],
        "modelArtifactSha256": metadata["artifactSha256"],
        "rulesVersion": RULE_BASELINE_VERSION,
        "trainingPerformed": False,
        "deploymentAuthorized": False,
        "deploymentDecision": "defer",
        "decisionReason": "evaluation_only_no_deployment",
        "cohort": {
            "totalTraceCount": len(manifest["traces"]),
            "pseudonymousGroupCount": len(groups),
            "labelEligibleCount": label_eligible,
            "featureReadyCount": feature_ready,
            "scoredTraceCount": len(predictions),
            "labelReviewCount": label_review,
            "featureReviewCount": feature_review,
            "missingFeatureCount": missing,
        },
        "rulesMetrics": _metrics(rule_rows),
        "modelMetrics": _metrics(model_rows),
        "predictions": predictions,
    }
    result["evaluationResultSha256"] = hashlib.sha256(canonical_json(result)).hexdigest()
    validate_field_evaluation_result(result)
    return result
