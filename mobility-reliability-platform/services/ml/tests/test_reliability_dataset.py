from __future__ import annotations

import copy
import math

import pytest

from mobility_ml.manifest import DatasetValidationError, canonical_json
from mobility_ml.reliability_dataset import (
    generate_reliability_dataset,
    reliability_dataset_hash,
    validate_reliability_dataset,
)


def test_reliability_dataset_is_deterministic_coordinate_free_and_group_disjoint() -> None:
    first = generate_reliability_dataset()
    second = generate_reliability_dataset()
    assert canonical_json(first) == canonical_json(second)
    assert reliability_dataset_hash(first) == reliability_dataset_hash(second)
    rendered = canonical_json(first).decode()
    assert all(
        forbidden not in rendered
        for forbidden in ("latitude", "longitude", "tenantId", "firebaseUid")
    )
    groups_by_split = {
        split: {row["deviceGroupId"] for row in first["episodes"] if row["split"] == split}
        for split in ("train", "validation", "test")
    }
    assert len(groups_by_split["test"]) < len(
        [row for row in first["episodes"] if row["split"] == "test"]
    )
    assert groups_by_split["train"].isdisjoint(groups_by_split["validation"])
    assert groups_by_split["validation"].isdisjoint(groups_by_split["test"])


def test_group_and_future_label_leakage_fail_closed() -> None:
    dataset = generate_reliability_dataset()
    leaked = copy.deepcopy(dataset)
    validation = next(
        row
        for row in leaked["episodes"]
        if row["split"] == "validation" and row["riskStartReason"] == "observation_started"
    )
    train = next(row for row in leaked["episodes"] if row["split"] == "train")
    validation["deviceGroupId"] = train["deviceGroupId"]
    with pytest.raises(DatasetValidationError, match="split_leakage"):
        validate_reliability_dataset(leaked)

    future = copy.deepcopy(dataset)
    train = next(
        row for row in future["episodes"] if row["split"] == "train" and not row["eventObserved"]
    )
    train["riskStartAt"] = "2024-07-28T00:00:00Z"
    train["decisionAt"] = "2025-01-25T00:00:00Z"
    train["observedThroughAt"] = "2025-02-24T00:00:00Z"
    with pytest.raises(DatasetValidationError, match="future_leakage"):
        validate_reliability_dataset(future)


def test_component_replacement_is_the_only_explicit_risk_clock_reset() -> None:
    dataset = generate_reliability_dataset()
    replaced = next(row for row in dataset["episodes"] if row.get("riskResetEventId"))
    assert replaced["riskStartReason"] == "component_replaced"
    invalid = copy.deepcopy(dataset)
    target = next(
        row for row in invalid["episodes"] if row["riskStartReason"] == "observation_started"
    )
    target["riskResetEventId"] = replaced["riskResetEventId"]
    with pytest.raises(DatasetValidationError, match="unexpected"):
        validate_reliability_dataset(invalid)

    missing_event = copy.deepcopy(dataset)
    missing_event["replacementEvents"] = [
        event
        for event in missing_event["replacementEvents"]
        if event["eventId"] != replaced["riskResetEventId"]
    ]
    with pytest.raises(DatasetValidationError, match="missing"):
        validate_reliability_dataset(missing_event)


def test_distance_features_are_finite_and_available_at_decision() -> None:
    future = copy.deepcopy(generate_reliability_dataset())
    future["episodes"][0]["distanceSummaryAsOfAt"] = future["episodes"][0]["observedThroughAt"]
    with pytest.raises(DatasetValidationError, match="future_leakage"):
        validate_reliability_dataset(future)
    non_finite = copy.deepcopy(generate_reliability_dataset())
    non_finite["episodes"][0]["meanDailyDistanceM"] = math.inf
    with pytest.raises(DatasetValidationError, match="invalid"):
        validate_reliability_dataset(non_finite)
