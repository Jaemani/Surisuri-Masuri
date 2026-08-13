"""R10 fixed-rule and Kaplan-Meier time-to-inspection baselines."""

from __future__ import annotations

import hashlib
import uuid
from collections import defaultdict
from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from typing import Any

from .manifest import DatasetValidationError, _validate_repository_schema, canonical_json
from .reliability_dataset import (
    DATASET_VERSION,
    HORIZON_DAYS,
    OUTCOME_VERSION,
    SPLIT_STRATEGY,
    reliability_dataset_hash,
    validate_reliability_dataset,
)

RESULT_VERSION = "reliability-baseline-result.v1"
MINIMUM_TRAIN_SAMPLES = 4
MINIMUM_TRAIN_EVENTS = 2
FIXED_INTERVAL_DAYS = 180
DISTANCE_THRESHOLD_M = 1_000_000
KM_RISK_THRESHOLD = 0.5


def _timestamp(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(UTC)


def _round(value: float) -> float:
    return round(float(value), 12)


def _age_days(episode: Mapping[str, Any]) -> int:
    return (_timestamp(episode["decisionAt"]) - _timestamp(episode["riskStartAt"])).days


def _grouped(dataset: Mapping[str, Any], split: str) -> dict[str, list[Mapping[str, Any]]]:
    grouped: dict[str, list[Mapping[str, Any]]] = defaultdict(list)
    for episode in dataset["episodes"]:
        if episode["split"] == split:
            grouped[episode["component"]].append(episode)
    return grouped


def _sufficiency(
    train_rows: list[Mapping[str, Any]], *, require_distance: bool = False
) -> str | None:
    if len(train_rows) < MINIMUM_TRAIN_SAMPLES:
        return "insufficient_samples"
    if sum(bool(row["eventObserved"]) for row in train_rows) < MINIMUM_TRAIN_EVENTS:
        return "insufficient_events"
    if require_distance and any(
        not isinstance(row.get(key), int | float)
        for row in train_rows
        for key in ("cumulativeDistanceM", "meanDailyDistanceM")
    ):
        return "data_quality_insufficient"
    return None


def _metrics(rows: list[Mapping[str, Any]], probabilities: list[float]) -> dict[str, Any]:
    actual = [bool(row["eventObserved"]) for row in rows]
    predicted = [probability >= KM_RISK_THRESHOLD for probability in probabilities]
    tp = sum(a and p for a, p in zip(actual, predicted, strict=True))
    fp = sum(not a and p for a, p in zip(actual, predicted, strict=True))
    tn = sum(not a and not p for a, p in zip(actual, predicted, strict=True))
    fn = sum(a and not p for a, p in zip(actual, predicted, strict=True))
    sensitivity = tp / (tp + fn) if tp + fn else 0.0
    specificity = tn / (tn + fp) if tn + fp else 0.0
    brier = sum(
        (probability - float(a)) ** 2 for probability, a in zip(probabilities, actual, strict=True)
    ) / len(rows)
    return {
        "dueCount": sum(predicted),
        "confusion": {
            "truePositive": tp,
            "falsePositive": fp,
            "trueNegative": tn,
            "falseNegative": fn,
        },
        "sensitivity": _round(sensitivity),
        "specificity": _round(specificity),
        "brierScore": _round(brier),
    }


def _component_result(
    component: str,
    train_rows: list[Mapping[str, Any]],
    test_rows: list[Mapping[str, Any]],
    probability: Callable[[Mapping[str, Any], list[Mapping[str, Any]]], float],
    *,
    require_distance: bool = False,
    include_survival: bool = False,
) -> dict[str, Any]:
    event_count = sum(bool(row["eventObserved"]) for row in test_rows)
    base: dict[str, Any] = {
        "component": component,
        "sampleCount": len(test_rows),
        "eventCount": event_count,
        "censoredCount": len(test_rows) - event_count,
        "minimumSampleCount": MINIMUM_TRAIN_SAMPLES,
        "minimumEventCount": MINIMUM_TRAIN_EVENTS,
    }
    reason = _sufficiency(train_rows, require_distance=require_distance)
    if (
        reason is None
        and require_distance
        and any(
            not isinstance(row.get(key), int | float)
            for row in test_rows
            for key in ("cumulativeDistanceM", "meanDailyDistanceM")
        )
    ):
        reason = "data_quality_insufficient"
    if reason is not None:
        return {
            **base,
            "status": "data_insufficient",
            "abstention": True,
            "abstentionReason": reason,
        }
    probabilities = [probability(row, train_rows) for row in test_rows]
    result = {
        **base,
        "status": "evaluated",
        "abstention": False,
        **_metrics(test_rows, probabilities),
    }
    if include_survival:
        result["survivalProbabilityAtHorizon"] = _round(1 - probabilities[0])
    return result


def _fixed_probability(row: Mapping[str, Any], _train: list[Mapping[str, Any]]) -> float:
    return float(_age_days(row) + HORIZON_DAYS >= FIXED_INTERVAL_DAYS)


def _distance_probability(row: Mapping[str, Any], _train: list[Mapping[str, Any]]) -> float:
    projected = row["cumulativeDistanceM"] + row["meanDailyDistanceM"] * HORIZON_DAYS
    return float(projected >= DISTANCE_THRESHOLD_M)


def _kaplan_meier_probability(
    _row: Mapping[str, Any], train_rows: list[Mapping[str, Any]]
) -> float:
    """Return 1-S(30) for equal-horizon right-censored synthetic episodes."""

    at_risk = len(train_rows)
    events_by_day: dict[int, int] = defaultdict(int)
    for train_row in train_rows:
        if train_row["eventObserved"]:
            day = (_timestamp(train_row["outcomeAt"]) - _timestamp(train_row["decisionAt"])).days
            events_by_day[day] += 1
    survival = 1.0
    for day in sorted(events_by_day):
        deaths = events_by_day[day]
        survival *= 1 - deaths / at_risk
        at_risk -= deaths
    return _round(1 - survival)


def _time_windows(dataset: Mapping[str, Any]) -> dict[str, dict[str, str]]:
    result: dict[str, dict[str, str]] = {}
    for split in ("train", "validation", "test"):
        rows = [row for row in dataset["episodes"] if row["split"] == split]
        result[split] = {
            "startAt": min(row["decisionAt"] for row in rows),
            "endAt": max(row["observedThroughAt"] for row in rows),
        }
    return result


def validate_reliability_result(result: Mapping[str, Any]) -> None:
    """Validate schema plus arithmetic and cross-method cohort invariants."""

    _validate_repository_schema(
        result,
        filename="reliability-baseline-result.v1.schema.json",
        error_type=DatasetValidationError,
        label=RESULT_VERSION,
    )
    expected_hash = result["resultSha256"]
    payload = {key: value for key, value in result.items() if key != "resultSha256"}
    if hashlib.sha256(canonical_json(payload)).hexdigest() != expected_hash:
        raise DatasetValidationError("reliability baseline result hash:mismatch")
    counts = result["counts"]
    if counts["observedOutcomes"] + counts["censored"] != counts["observations"]:
        raise DatasetValidationError("reliability baseline counts:not_reconciled")
    if counts["devices"] > counts["observations"]:
        raise DatasetValidationError("reliability baseline device count:impossible")
    windows = result["timeWindows"]
    if not _timestamp(windows["train"]["endAt"]) < _timestamp(windows["validation"]["startAt"]):
        raise DatasetValidationError("reliability baseline windows:train_validation_overlap")
    if not _timestamp(windows["validation"]["endAt"]) < _timestamp(windows["test"]["startAt"]):
        raise DatasetValidationError("reliability baseline windows:validation_test_overlap")
    cohort: dict[str, tuple[int, int, int]] | None = None
    abstained_components: set[str] = set()
    for method in result["methods"].values():
        current: dict[str, tuple[int, int, int]] = {}
        for component in method["components"]:
            code = component["component"]
            if code in current:
                raise DatasetValidationError("reliability baseline component:duplicate")
            triple = (
                component["sampleCount"],
                component["eventCount"],
                component["censoredCount"],
            )
            if triple[1] + triple[2] != triple[0]:
                raise DatasetValidationError("reliability baseline component counts:not_reconciled")
            current[code] = triple
            if component["abstention"]:
                abstained_components.add(code)
            else:
                confusion = component["confusion"]
                if sum(confusion.values()) != component["sampleCount"]:
                    raise DatasetValidationError("reliability baseline confusion:not_reconciled")
                if (
                    confusion["truePositive"] + confusion["falseNegative"]
                    != component["eventCount"]
                ):
                    raise DatasetValidationError("reliability baseline event count:mismatch")
                if confusion["truePositive"] + confusion["falsePositive"] != component["dueCount"]:
                    raise DatasetValidationError("reliability baseline due count:mismatch")
        if cohort is None:
            cohort = current
        elif cohort != current:
            raise DatasetValidationError("reliability baseline method cohort:mismatch")
    assert cohort is not None
    if (
        counts["observations"] != sum(triple[0] for triple in cohort.values())
        or counts["observedOutcomes"] != sum(triple[1] for triple in cohort.values())
        or counts["censored"] != sum(triple[2] for triple in cohort.values())
    ):
        raise DatasetValidationError("reliability baseline top-level cohort:mismatch")
    expected_abstained = sum(cohort[component][0] for component in abstained_components)
    if counts["abstained"] != expected_abstained:
        raise DatasetValidationError("reliability baseline abstention count:mismatch")


def evaluate_reliability_baselines(
    dataset: Mapping[str, Any], *, generated_at: str = "2026-08-13T12:00:00Z"
) -> dict[str, Any]:
    """Evaluate frozen train-derived policies on the untouched synthetic test split."""

    validate_reliability_dataset(dataset)
    dataset_hash = reliability_dataset_hash(dataset)
    train = _grouped(dataset, "train")
    test = _grouped(dataset, "test")
    components = sorted(test)

    def evaluate_method(
        probability: Callable[[Mapping[str, Any], list[Mapping[str, Any]]], float],
        *,
        require_distance: bool = False,
        include_survival: bool = False,
    ) -> list[dict[str, Any]]:
        return [
            _component_result(
                component,
                train.get(component, []),
                test[component],
                probability,
                require_distance=require_distance,
                include_survival=include_survival,
            )
            for component in components
        ]

    fixed = evaluate_method(_fixed_probability)
    distance = evaluate_method(_distance_probability, require_distance=True)
    kaplan_meier = evaluate_method(_kaplan_meier_probability, include_survival=True)
    test_rows = [row for rows in test.values() for row in rows]
    abstained_components = {
        result["component"]
        for method in (fixed, distance, kaplan_meier)
        for result in method
        if result["abstention"]
    }
    event_count = sum(bool(row["eventObserved"]) for row in test_rows)
    result: dict[str, Any] = {
        "schemaVersion": RESULT_VERSION,
        "evaluationId": str(uuid.uuid5(uuid.NAMESPACE_URL, f"r10:{dataset_hash}")),
        "generatedAt": generated_at,
        "evaluationScope": "synthetic_only",
        "sourceKind": "synthetic",
        "trainingPerformed": False,
        "datasetVersion": DATASET_VERSION,
        "datasetHash": dataset_hash,
        "outcomeDefinitionVersion": OUTCOME_VERSION,
        "splitStrategy": SPLIT_STRATEGY,
        "timeWindows": _time_windows(dataset),
        "groupLeakageDetected": False,
        "counts": {
            "devices": len({row["deviceGroupId"] for row in test_rows}),
            "observations": len(test_rows),
            "observedOutcomes": event_count,
            "censored": len(test_rows) - event_count,
            "abstained": sum(1 for row in test_rows if row["component"] in abstained_components),
            "countsReconciled": True,
        },
        "methods": {
            "fixedInterval": {
                "method": "fixed_interval",
                "threshold": {"intervalDays": FIXED_INTERVAL_DAYS},
                "components": fixed,
            },
            "cumulativeDistance": {
                "method": "cumulative_distance",
                "threshold": {"distanceMeters": DISTANCE_THRESHOLD_M},
                "components": distance,
            },
            "kaplanMeier": {
                "method": "kaplan_meier",
                "threshold": {"horizonDays": HORIZON_DAYS, "riskThreshold": KM_RISK_THRESHOLD},
                "components": kaplan_meier,
            },
        },
        "limitations": [
            "synthetic_data_only",
            "no_field_performance",
            "no_component_inference_without_explicit_linkage",
            "not_for_safety_critical_failure_prediction",
        ],
        "deploymentAuthorized": False,
        "deploymentDecision": "defer",
    }
    result["resultSha256"] = hashlib.sha256(canonical_json(result)).hexdigest()
    validate_reliability_result(result)
    return result
