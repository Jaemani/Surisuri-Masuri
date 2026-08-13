"""R11 aggregate calibration and abstention assessment for synthetic reliability data."""

from __future__ import annotations

import hashlib
import uuid
from collections.abc import Mapping
from typing import Any

from .manifest import DatasetValidationError, _validate_repository_schema, canonical_json
from .reliability_baseline import (
    KM_RISK_THRESHOLD,
    _grouped,
    _kaplan_meier_probability,
    _round,
    _sufficiency,
    validate_reliability_result,
)
from .reliability_dataset import (
    COMPONENTS,
    HORIZON_DAYS,
    reliability_dataset_hash,
    validate_reliability_dataset,
)

ASSESSMENT_VERSION = "reliability-calibration-assessment.v1"
SCHEMA_FILENAME = "reliability-calibration-assessment.v1.schema.json"
EVALUATOR_VERSION = "r11-calibration-estimability.v1"
POLICY_VERSION = "r11-calibration-abstention-policy.v1"
MINIMUM_CALIBRATION_SAMPLES = 30
MINIMUM_CALIBRATION_EVENTS = 10
MINIMUM_DISTINCT_SCORES = 3


def _hash(assessment: Mapping[str, Any]) -> str:
    payload = {key: value for key, value in assessment.items() if key != "assessmentSha256"}
    return hashlib.sha256(canonical_json(payload)).hexdigest()


def _metrics(split: str, rows: list[Mapping[str, Any]], predicted_risk: float) -> dict[str, Any]:
    actual = [float(bool(row["eventObserved"])) for row in rows]
    observed_rate = sum(actual) / len(actual)
    brier = sum((predicted_risk - value) ** 2 for value in actual) / len(actual)
    event_count = int(sum(actual))
    return {
        "split": split,
        "sampleCount": len(rows),
        "eventCount": event_count,
        "censoredCount": len(rows) - event_count,
        "predictedRisk": _round(predicted_risk),
        "observedRate": _round(observed_rate),
        "absoluteCalibrationError": _round(abs(predicted_risk - observed_rate)),
        "brierScore": _round(brier),
    }


def validate_reliability_calibration(
    assessment: Mapping[str, Any],
    *,
    dataset: Mapping[str, Any] | None = None,
    result: Mapping[str, Any] | None = None,
) -> None:
    """Validate schema, lineage, arithmetic, split policy, and self hash."""

    if (dataset is None) != (result is None):
        raise DatasetValidationError(
            "reliability calibration provenance:dataset_and_result_required"
        )
    _validate_repository_schema(
        assessment,
        filename=SCHEMA_FILENAME,
        error_type=DatasetValidationError,
        label=ASSESSMENT_VERSION,
    )
    if _hash(assessment) != assessment["assessmentSha256"]:
        raise DatasetValidationError("reliability calibration hash:mismatch")
    seen: set[str] = set()
    for component in assessment["components"]:
        code = component["component"]
        if code in seen:
            raise DatasetValidationError("reliability calibration component:duplicate")
        seen.add(code)
        if (
            component["validationEventCount"] > component["validationCount"]
            or component["testEventCount"] > component["testCount"]
        ):
            raise DatasetValidationError("reliability calibration component counts:impossible")
        if component["abstention"]:
            continue
        for split in ("validation", "test"):
            metric = component[split]
            if metric["split"] != split:
                raise DatasetValidationError("reliability calibration split:mismatch")
            if metric["eventCount"] + metric["censoredCount"] != metric["sampleCount"]:
                raise DatasetValidationError("reliability calibration counts:not_reconciled")
            expected_rate = metric["eventCount"] / metric["sampleCount"]
            if abs(metric["observedRate"] - expected_rate) > 1e-12:
                raise DatasetValidationError("reliability calibration observed_rate:mismatch")
            expected_error = abs(metric["predictedRisk"] - metric["observedRate"])
            if abs(metric["absoluteCalibrationError"] - expected_error) > 1e-12:
                raise DatasetValidationError("reliability calibration error:mismatch")
    if dataset is not None and result is not None:
        validate_reliability_dataset(dataset)
        validate_reliability_result(result)
        dataset_hash = reliability_dataset_hash(dataset)
        if assessment["lineage"] != {
            "datasetSha256": dataset_hash,
            "baselineResultSha256": result["resultSha256"],
        }:
            raise DatasetValidationError("reliability calibration lineage:mismatch")
        if assessment["factBoundary"]["explicitRiskResetFactCount"] != len(
            dataset["replacementEvents"]
        ):
            raise DatasetValidationError("reliability calibration fact count:mismatch")


def assess_reliability_calibration(
    dataset: Mapping[str, Any],
    result: Mapping[str, Any],
    *,
    generated_at: str = "2026-08-13T13:00:00Z",
) -> dict[str, Any]:
    """Measure train-derived KM calibration on validation and untouched test splits."""

    validate_reliability_dataset(dataset)
    validate_reliability_result(result)
    dataset_hash = reliability_dataset_hash(dataset)
    if result["datasetHash"] != dataset_hash:
        raise DatasetValidationError("reliability calibration result datasetHash:mismatch")
    grouped = {split: _grouped(dataset, split) for split in ("train", "validation", "test")}
    components: list[dict[str, Any]] = []
    for component in COMPONENTS:
        train_rows = grouped["train"][component]
        validation_rows = grouped["validation"][component]
        test_rows = grouped["test"][component]
        predicted_scores = (
            [_kaplan_meier_probability(row, train_rows) for row in validation_rows]
            if _sufficiency(train_rows) is None
            else []
        )
        distinct_scores = len(set(predicted_scores))
        train_reason = _sufficiency(train_rows)
        reason = (
            "reliability_train_insufficient"
            if train_reason is not None
            else "calibration_sample_insufficient"
            if len(validation_rows) < MINIMUM_CALIBRATION_SAMPLES
            else "calibration_event_insufficient"
            if sum(bool(row["eventObserved"]) for row in validation_rows)
            < MINIMUM_CALIBRATION_EVENTS
            else "score_variation_insufficient"
            if distinct_scores < MINIMUM_DISTINCT_SCORES
            else None
        )
        base = {
            "component": component,
            "validationCount": len(validation_rows),
            "validationEventCount": sum(bool(row["eventObserved"]) for row in validation_rows),
            "testCount": len(test_rows),
            "testEventCount": sum(bool(row["eventObserved"]) for row in test_rows),
            "distinctValidationScoreCount": distinct_scores,
            "fallback": "fixed_interval_and_human_review",
        }
        if reason is not None:
            components.append(
                {
                    **base,
                    "calibrationStatus": "not_estimable",
                    "abstention": True,
                    "notEstimableReason": reason,
                }
            )
            continue
        predicted_risk = predicted_scores[0]
        components.append(
            {
                **base,
                "calibrationStatus": "evaluated",
                "abstention": False,
                "validation": _metrics("validation", validation_rows, predicted_risk),
                "test": _metrics("test", test_rows, predicted_risk),
                "individualActionAllowed": False,
            }
        )
    assessment: dict[str, Any] = {
        "schemaVersion": ASSESSMENT_VERSION,
        "assessmentId": str(uuid.uuid5(uuid.NAMESPACE_URL, f"r11:{dataset_hash}")),
        "generatedAt": generated_at,
        "evaluationScope": "synthetic_only",
        "sourceKind": "synthetic",
        "evaluatorVersion": EVALUATOR_VERSION,
        "policyVersion": POLICY_VERSION,
        "lineage": {
            "datasetSha256": dataset_hash,
            "baselineResultSha256": result["resultSha256"],
        },
        "assessmentPolicy": {
            "method": "kaplan_meier",
            "horizonDays": HORIZON_DAYS,
            "riskThreshold": KM_RISK_THRESHOLD,
            "minimumSamples": MINIMUM_CALIBRATION_SAMPLES,
            "minimumEvents": MINIMUM_CALIBRATION_EVENTS,
            "minimumDistinctScores": MINIMUM_DISTINCT_SCORES,
            "validationPurpose": "calibration_and_abstention_assessment",
            "testPurpose": "untouched_final_measurement",
            "testUsedForTuning": False,
        },
        "factBoundary": {
            "riskResetSourceQuality": "verified_synthetic",
            "explicitRiskResetFactCount": len(dataset["replacementEvents"]),
            "componentLinkInferenceAllowed": False,
            "rawRepairTextIncluded": False,
            "identityIncluded": False,
        },
        "components": components,
        "limitations": [
            "synthetic_data_only",
            "no_field_calibration",
            "aggregate_only",
            "no_individual_action",
            "not_for_safety_critical_failure_prediction",
        ],
        "deploymentAuthorized": False,
        "deploymentDecision": "defer",
    }
    assessment["assessmentSha256"] = _hash(assessment)
    validate_reliability_calibration(assessment, dataset=dataset, result=result)
    return assessment
