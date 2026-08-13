from __future__ import annotations

import copy
import json
from pathlib import Path

import pytest

from mobility_ml.manifest import DatasetValidationError
from mobility_ml.reliability_baseline import evaluate_reliability_baselines
from mobility_ml.reliability_calibration import (
    assess_reliability_calibration,
    validate_reliability_calibration,
)
from mobility_ml.reliability_dataset import generate_reliability_dataset


def assessment() -> dict:
    dataset = generate_reliability_dataset()
    return assess_reliability_calibration(dataset, evaluate_reliability_baselines(dataset))


def test_small_single_score_cohorts_fail_closed_without_calibration_metrics() -> None:
    result = assessment()
    assert result["evaluationScope"] == "synthetic_only"
    assert result["deploymentAuthorized"] is False
    assert result["assessmentPolicy"]["testUsedForTuning"] is False
    assert all(
        component["calibrationStatus"] == "not_estimable" for component in result["components"]
    )
    assert all(component["abstention"] is True for component in result["components"])
    assert all(
        "validation" not in component and "test" not in component
        for component in result["components"]
    )
    assert {component["notEstimableReason"] for component in result["components"]} == {
        "calibration_sample_insufficient",
        "reliability_train_insufficient",
    }


def test_assessment_is_aggregate_identity_free_and_explicit_fact_bound() -> None:
    result = assessment()
    rendered = json.dumps(result, sort_keys=True)
    assert result["factBoundary"] == {
        "riskResetSourceQuality": "verified_synthetic",
        "explicitRiskResetFactCount": 6,
        "componentLinkInferenceAllowed": False,
        "rawRepairTextIncluded": False,
        "identityIncluded": False,
    }
    assert all(
        forbidden not in rendered
        for forbidden in ("deviceGroupId", "episodeId", "riskResetEventId", "latitude", "longitude")
    )


def test_test_label_mutation_cannot_make_an_ineligible_component_estimable() -> None:
    dataset = generate_reliability_dataset()
    baseline = evaluate_reliability_baselines(dataset)
    original = assess_reliability_calibration(dataset, baseline)
    mutated = copy.deepcopy(dataset)
    for row in mutated["episodes"]:
        if row["split"] == "test":
            row["eventObserved"] = False
            row.pop("outcomeAt", None)
            row.pop("outcomeKind", None)
    changed = assess_reliability_calibration(mutated, evaluate_reliability_baselines(mutated))
    assert [item["calibrationStatus"] for item in changed["components"]] == [
        item["calibrationStatus"] for item in original["components"]
    ]
    assert [item["notEstimableReason"] for item in changed["components"]] == [
        item["notEstimableReason"] for item in original["components"]
    ]


def test_hash_and_lineage_tampering_are_rejected() -> None:
    dataset = generate_reliability_dataset()
    baseline = evaluate_reliability_baselines(dataset)
    result = assess_reliability_calibration(dataset, baseline)
    tampered = copy.deepcopy(result)
    tampered["components"][0]["validationCount"] += 1
    with pytest.raises(DatasetValidationError, match="hash:mismatch"):
        validate_reliability_calibration(tampered)
    wrong_lineage = copy.deepcopy(result)
    wrong_lineage["lineage"]["datasetSha256"] = "0" * 64
    with pytest.raises(DatasetValidationError, match="hash:mismatch"):
        validate_reliability_calibration(wrong_lineage, dataset=dataset, result=baseline)


def test_console_snapshot_is_exactly_the_generated_assessment() -> None:
    expected = assessment()
    snapshot = json.loads(
        (
            Path(__file__).parents[3]
            / "packages/contracts/fixtures/reliability-calibration-assessment.v1.valid.json"
        ).read_text(encoding="utf-8")
    )
    assert snapshot == expected
