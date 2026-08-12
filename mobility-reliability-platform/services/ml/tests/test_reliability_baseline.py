from __future__ import annotations

import copy
import json

import pytest

from mobility_ml.manifest import DatasetValidationError
from mobility_ml.reliability_baseline import (
    DISTANCE_THRESHOLD_M,
    FIXED_INTERVAL_DAYS,
    _distance_probability,
    _fixed_probability,
    evaluate_reliability_baselines,
    validate_reliability_result,
)
from mobility_ml.reliability_dataset import generate_reliability_dataset


def test_three_baselines_are_deterministic_synthetic_only_and_deferred() -> None:
    dataset = generate_reliability_dataset()
    first = evaluate_reliability_baselines(dataset)
    second = evaluate_reliability_baselines(dataset)
    assert first == second
    assert first["evaluationScope"] == "synthetic_only"
    assert first["trainingPerformed"] is False
    assert first["deploymentAuthorized"] is False
    assert first["deploymentDecision"] == "defer"
    assert set(first["methods"]) == {"fixedInterval", "cumulativeDistance", "kaplanMeier"}
    assert (
        first["counts"]["observedOutcomes"] + first["counts"]["censored"]
        == first["counts"]["observations"]
    )
    rendered = json.dumps(first, sort_keys=True)
    assert all(
        forbidden not in rendered
        for forbidden in ("deviceGroupId", "episodeId", "outcomeAt", "latitude", "longitude")
    )


def test_fixed_and_distance_horizon_boundaries_are_explicit() -> None:
    row = {
        "riskStartAt": "2026-01-01T00:00:00Z",
        "decisionAt": "2026-06-01T00:00:00Z",
        "cumulativeDistanceM": DISTANCE_THRESHOLD_M - 30_000,
        "meanDailyDistanceM": 1_000,
    }
    assert _fixed_probability(row, []) == float(FIXED_INTERVAL_DAYS <= 151 + 30)
    assert _distance_probability(row, []) == 1.0
    row["cumulativeDistanceM"] -= 1
    assert _distance_probability(row, []) == 0.0


def test_train_evidence_controls_abstention_without_emitting_metrics() -> None:
    result = evaluate_reliability_baselines(generate_reliability_dataset())
    for method in result["methods"].values():
        controller = next(
            component
            for component in method["components"]
            if component["component"] == "controller"
        )
        assert controller["status"] == "data_insufficient"
        assert controller["abstention"] is True
        assert not {
            "dueCount",
            "confusion",
            "brierScore",
            "survivalProbabilityAtHorizon",
        }.intersection(controller)


def test_test_label_mutation_does_not_change_train_derived_km_probability() -> None:
    dataset = generate_reliability_dataset()
    original = evaluate_reliability_baselines(dataset)
    mutated = copy.deepcopy(dataset)
    for row in mutated["episodes"]:
        if row["split"] != "test":
            continue
        if row["eventObserved"]:
            row["eventObserved"] = False
            del row["outcomeAt"]
            del row["outcomeKind"]
        else:
            row["eventObserved"] = True
            row["outcomeAt"] = row["decisionAt"]
            row["outcomeKind"] = "inspection_required"
    changed = evaluate_reliability_baselines(mutated)
    for component in ("battery", "brake"):
        before = next(
            row
            for row in original["methods"]["kaplanMeier"]["components"]
            if row["component"] == component
        )
        after = next(
            row
            for row in changed["methods"]["kaplanMeier"]["components"]
            if row["component"] == component
        )
        assert before["survivalProbabilityAtHorizon"] == after["survivalProbabilityAtHorizon"]


def test_result_semantics_reject_count_and_method_cohort_tampering() -> None:
    result = evaluate_reliability_baselines(generate_reliability_dataset())
    bad_counts = copy.deepcopy(result)
    bad_counts["counts"]["censored"] += 1
    with pytest.raises(DatasetValidationError, match="counts:not_reconciled"):
        validate_reliability_result(bad_counts)
    bad_cohort = copy.deepcopy(result)
    bad_cohort["methods"]["fixedInterval"]["components"][0]["sampleCount"] += 1
    with pytest.raises(DatasetValidationError, match="component counts:not_reconciled"):
        validate_reliability_result(bad_cohort)
    disconnected_top_level = copy.deepcopy(result)
    disconnected_top_level["counts"]["observations"] += 1
    disconnected_top_level["counts"]["censored"] += 1
    with pytest.raises(DatasetValidationError, match="top-level cohort:mismatch"):
        validate_reliability_result(disconnected_top_level)
    impossible_devices = copy.deepcopy(result)
    impossible_devices["counts"]["devices"] = impossible_devices["counts"]["observations"] + 1
    with pytest.raises(DatasetValidationError, match="device count:impossible"):
        validate_reliability_result(impossible_devices)


def test_equal_horizon_kaplan_meier_known_answer() -> None:
    dataset = generate_reliability_dataset()
    result = evaluate_reliability_baselines(dataset)
    train_battery = [
        row
        for row in dataset["episodes"]
        if row["split"] == "train" and row["component"] == "battery"
    ]
    event_rate = sum(row["eventObserved"] for row in train_battery) / len(train_battery)
    battery = next(
        row
        for row in result["methods"]["kaplanMeier"]["components"]
        if row["component"] == "battery"
    )
    assert battery["survivalProbabilityAtHorizon"] == pytest.approx(1 - event_rate)
