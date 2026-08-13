"""Identity-free aggregate presentation artifact for the R10 comparison."""

from __future__ import annotations

import hashlib
import math
from collections import defaultdict
from collections.abc import Mapping
from datetime import UTC, datetime
from typing import Any

from .manifest import DatasetValidationError, _validate_repository_schema, canonical_json
from .reliability_baseline import (
    _sufficiency,
    validate_reliability_result,
)
from .reliability_dataset import (
    COMPONENTS,
    DATASET_VERSION,
    HORIZON_DAYS,
    reliability_dataset_hash,
    validate_reliability_dataset,
)

PRESENTATION_VERSION = "reliability-comparison-artifact.v1"
ARTIFACT_VERSION = "r10-reliability-presentation.v1"
PRESENTATION_SCHEMA_FILENAME = "reliability-comparison-artifact.v1.schema.json"

_FORBIDDEN_KEYS = {
    "latitude",
    "longitude",
    "coordinates",
    "deviceId",
    "deviceGroupId",
    "episodeId",
    "outcomeAt",
    "riskResetEventId",
    "tenantId",
    "firebaseUid",
    "userId",
    "phoneNumber",
    "repairMemo",
}
_METHODS = ("fixedInterval", "cumulativeDistance", "kaplanMeier")


def _timestamp(value: Any, label: str) -> datetime:
    if not isinstance(value, str):
        raise DatasetValidationError(f"reliability presentation {label}:date-time")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise DatasetValidationError(f"reliability presentation {label}:date-time") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise DatasetValidationError(f"reliability presentation {label}:date-time")
    return parsed.astimezone(UTC)


def _contains_forbidden_key(value: Any) -> bool:
    if isinstance(value, Mapping):
        return bool(_FORBIDDEN_KEYS.intersection(value)) or any(
            _contains_forbidden_key(child) for child in value.values()
        )
    if isinstance(value, list):
        return any(_contains_forbidden_key(child) for child in value)
    return False


def _without_hash(artifact: Mapping[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in artifact.items() if key != "artifactSha256"}


def _artifact_hash(artifact: Mapping[str, Any]) -> str:
    try:
        return hashlib.sha256(canonical_json(_without_hash(artifact))).hexdigest()
    except (TypeError, ValueError, OverflowError) as error:
        raise DatasetValidationError("reliability presentation hash:unserializable") from error


def _km_curve(rows: list[Mapping[str, Any]]) -> dict[str, Any]:
    """Build only the train Kaplan--Meier curve, without source identities."""

    event_count = sum(bool(row["eventObserved"]) for row in rows)
    base = {
        "sampleCount": len(rows),
        "eventCount": event_count,
        "censoredCount": len(rows) - event_count,
    }
    reason = _sufficiency(rows)
    if reason is not None:
        return {**base, "status": "data_insufficient", "abstention": True}

    events_by_day: dict[int, int] = defaultdict(int)
    for row in rows:
        if row["eventObserved"]:
            day = (
                _timestamp(row["outcomeAt"], "outcomeAt")
                - _timestamp(row["decisionAt"], "decisionAt")
            ).days
            events_by_day[day] += 1

    at_risk = len(rows)
    survival = 1.0
    points: list[dict[str, Any]] = [{"day": 0, "eventFreeProbability": 1.0}]
    for day in sorted(events_by_day):
        deaths = events_by_day[day]
        survival *= 1 - deaths / at_risk
        at_risk -= deaths
        if day == 0:
            points[0] = {"day": 0, "eventFreeProbability": round(survival, 12)}
        else:
            points.append({"day": day, "eventFreeProbability": round(survival, 12)})
    if points[-1]["day"] != HORIZON_DAYS:
        points.append({"day": HORIZON_DAYS, "eventFreeProbability": round(survival, 12)})
    return {
        **base,
        "status": "evaluated",
        "abstention": False,
        "curve": {"sourceSplit": "train", "points": points},
    }


def _validate_curve(curve: Mapping[str, Any]) -> None:
    if curve.get("sourceSplit") != "train":
        raise DatasetValidationError("reliability presentation curve:source_split")
    points = curve.get("points")
    if not isinstance(points, list) or len(points) < 2:
        raise DatasetValidationError("reliability presentation curve:points")
    previous_day = -1
    previous_probability = 1.0
    for point in points:
        day = point.get("day")
        probability = point.get("eventFreeProbability")
        if (
            not isinstance(day, int)
            or isinstance(day, bool)
            or day <= previous_day
            or not 0 <= day <= HORIZON_DAYS
            or not isinstance(probability, int | float)
            or isinstance(probability, bool)
            or not math.isfinite(float(probability))
            or not 0 <= probability <= 1
            or probability > previous_probability
        ):
            raise DatasetValidationError("reliability presentation curve:invalid")
        previous_day = day
        previous_probability = float(probability)
    if points[0]["day"] != 0 or points[-1]["day"] != HORIZON_DAYS:
        raise DatasetValidationError("reliability presentation curve:horizon")


def _metric_projection(
    method: Mapping[str, Any], *, curve: Mapping[str, Any] | None
) -> dict[str, Any]:
    status = method["status"]
    if status == "data_insufficient":
        return {
            "status": status,
            "abstention": True,
            "abstentionReason": method["abstentionReason"],
        }
    projection = {
        "status": status,
        "abstention": False,
        "dueCount": method["dueCount"],
        "confusion": method["confusion"],
        "sensitivity": method["sensitivity"],
        "specificity": method["specificity"],
        "brierScore": method["brierScore"],
    }
    if curve is not None:
        projection["curve"] = curve
    return projection


def validate_reliability_presentation(
    artifact: Mapping[str, Any],
    *,
    dataset: Mapping[str, Any] | None = None,
    result: Mapping[str, Any] | None = None,
) -> None:
    """Validate schema, self hash, lineage, metric binding, and privacy shape."""

    if not isinstance(artifact, Mapping):
        raise DatasetValidationError("reliability presentation:object")
    if (dataset is None) != (result is None):
        raise DatasetValidationError(
            "reliability presentation provenance:dataset_and_result_required"
        )
    _validate_repository_schema(
        artifact,
        filename=PRESENTATION_SCHEMA_FILENAME,
        error_type=DatasetValidationError,
        label=PRESENTATION_VERSION,
    )
    if _artifact_hash(artifact) != artifact.get("artifactSha256"):
        raise DatasetValidationError("reliability presentation artifactSha256:mismatch")
    if _contains_forbidden_key(artifact):
        raise DatasetValidationError("reliability presentation identity_or_location:forbidden")
    if artifact["comparisonContext"] != {
        "metricSplit": "test",
        "curveSourceSplit": "train",
        "horizonDays": HORIZON_DAYS,
        "validationUsedForTuning": False,
    }:
        raise DatasetValidationError("reliability presentation comparison_context:mismatch")
    components = artifact["components"]
    seen: set[str] = set()
    for component in components:
        code = component["component"]
        if code in seen:
            raise DatasetValidationError("reliability presentation component:duplicate")
        seen.add(code)
        if component["eventCount"] + component["censoredCount"] != component["sampleCount"]:
            raise DatasetValidationError("reliability presentation component:counts")
        methods = component["methods"]
        for method_name in _METHODS:
            method = methods[method_name]
            if method["status"] == "data_insufficient":
                if method["abstention"] is not True or "curve" in method:
                    raise DatasetValidationError("reliability presentation abstention:invalid")
            elif method["abstention"] is not False:
                raise DatasetValidationError("reliability presentation evaluated:abstention")
        km = methods["kaplanMeier"]
        if km["status"] == "evaluated":
            _validate_curve(km["curve"])
    if dataset is not None:
        validate_reliability_dataset(dataset)
        if artifact["lineage"]["datasetSha256"] != reliability_dataset_hash(dataset):
            raise DatasetValidationError("reliability presentation datasetHash:mismatch")
        for component in components:
            train_rows = [
                row
                for row in dataset["episodes"]
                if row["split"] == "train" and row["component"] == component["component"]
            ]
            expected_curve = _km_curve(train_rows).get("curve")
            actual_curve = component["methods"]["kaplanMeier"].get("curve")
            if actual_curve != expected_curve:
                raise DatasetValidationError("reliability presentation curve:dataset_binding")
    if result is not None:
        validate_reliability_result(result)
        if artifact["lineage"]["datasetSha256"] != result["datasetHash"]:
            raise DatasetValidationError("reliability presentation result datasetHash:mismatch")
        if artifact["lineage"]["baselineResultSha256"] != result["resultSha256"]:
            raise DatasetValidationError("reliability presentation baselineResultSha256:mismatch")
        expected_components = {
            method_component["component"]: {
                method_name: method_component for method_name in _METHODS
            }
            for method_name in _METHODS
            for method_component in result["methods"][method_name]["components"]
        }
        if {component["component"] for component in components} != set(expected_components):
            raise DatasetValidationError("reliability presentation component:cohort")
        for component in components:
            code = component["component"]
            expected_by_method = {
                method_name: next(
                    item
                    for item in result["methods"][method_name]["components"]
                    if item["component"] == code
                )
                for method_name in _METHODS
            }
            first = expected_by_method["fixedInterval"]
            if (component["sampleCount"], component["eventCount"], component["censoredCount"]) != (
                first["sampleCount"],
                first["eventCount"],
                first["censoredCount"],
            ):
                raise DatasetValidationError("reliability presentation component:binding")
            for method_name in _METHODS:
                expected = _metric_projection(
                    expected_by_method[method_name],
                    curve=component["methods"][method_name].get("curve")
                    if method_name == "kaplanMeier"
                    else None,
                )
                if component["methods"][method_name] != expected:
                    raise DatasetValidationError("reliability presentation method:binding")
            expected_km = expected_by_method["kaplanMeier"]
            if expected_km["status"] == "evaluated":
                final_probability = component["methods"]["kaplanMeier"]["curve"]["points"][-1][
                    "eventFreeProbability"
                ]
                if not math.isclose(
                    final_probability,
                    expected_km["survivalProbabilityAtHorizon"],
                    rel_tol=0,
                    abs_tol=1e-12,
                ):
                    raise DatasetValidationError("reliability presentation curve:horizon_binding")


def build_reliability_presentation(
    dataset: Mapping[str, Any], result: Mapping[str, Any]
) -> dict[str, Any]:
    """Build the deterministic aggregate artifact for internal synthetic display."""

    validate_reliability_dataset(dataset)
    validate_reliability_result(result)
    dataset_hash = reliability_dataset_hash(dataset)
    if result["datasetHash"] != dataset_hash:
        raise DatasetValidationError("reliability presentation result datasetHash:mismatch")

    train_rows = {
        component: [
            row
            for row in dataset["episodes"]
            if row["split"] == "train" and row["component"] == component
        ]
        for component in COMPONENTS
    }
    result_by_method = {
        method_name: {row["component"]: row for row in result["methods"][method_name]["components"]}
        for method_name in _METHODS
    }
    components: list[dict[str, Any]] = []
    for component in COMPONENTS:
        fixed = result_by_method["fixedInterval"][component]
        km_curve = _km_curve(train_rows[component]).get("curve")
        methods = {
            method_name: _metric_projection(
                result_by_method[method_name][component],
                curve=km_curve if method_name == "kaplanMeier" else None,
            )
            for method_name in _METHODS
        }
        components.append(
            {
                "component": component,
                "sampleCount": fixed["sampleCount"],
                "eventCount": fixed["eventCount"],
                "censoredCount": fixed["censoredCount"],
                "methods": methods,
            }
        )
    artifact: dict[str, Any] = {
        "schemaVersion": PRESENTATION_VERSION,
        "artifactVersion": ARTIFACT_VERSION,
        "evaluationScope": "synthetic_only",
        "sourceKind": "synthetic",
        "datasetVersion": DATASET_VERSION,
        "lineage": {
            "datasetSha256": dataset_hash,
            "baselineResultSha256": result["resultSha256"],
        },
        "comparisonContext": {
            "metricSplit": "test",
            "curveSourceSplit": "train",
            "horizonDays": HORIZON_DAYS,
            "validationUsedForTuning": False,
        },
        "displayPolicy": {
            "audience": "internal_synthetic_demo",
            "presentationMode": "read_only",
            "perDeviceInference": False,
            "fieldPerformance": False,
            "productionUse": False,
            "safetyDecision": False,
        },
        "components": components,
        "limitations": list(result["limitations"]),
        "deploymentAuthorized": False,
        "deploymentDecision": "defer",
    }
    artifact["artifactSha256"] = _artifact_hash(artifact)
    validate_reliability_presentation(artifact, dataset=dataset, result=result)
    return artifact


def reliability_presentation_hash(artifact: Mapping[str, Any]) -> str:
    """Return the canonical hash after validating the presentation artifact."""

    validate_reliability_presentation(artifact)
    return artifact["artifactSha256"]
