from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path

import pytest

import mobility_ml.reliability_presentation as presentation
from mobility_ml.manifest import DatasetValidationError, canonical_json
from mobility_ml.reliability_baseline import evaluate_reliability_baselines
from mobility_ml.reliability_dataset import generate_reliability_dataset


def _inputs() -> tuple[dict, dict]:
    dataset = generate_reliability_dataset()
    result = evaluate_reliability_baselines(dataset)
    return dataset, result


def _rehash(artifact: dict) -> None:
    payload = {key: value for key, value in artifact.items() if key != "artifactSha256"}
    artifact["artifactSha256"] = hashlib.sha256(canonical_json(payload)).hexdigest()


def test_presentation_separates_train_curve_and_test_metrics() -> None:
    dataset, result = _inputs()
    artifact = presentation.build_reliability_presentation(dataset, result)

    assert artifact["comparisonContext"] == {
        "metricSplit": "test",
        "curveSourceSplit": "train",
        "horizonDays": 30,
        "validationUsedForTuning": False,
    }
    assert set(component["component"] for component in artifact["components"]) == {
        "battery",
        "brake",
        "controller",
    }
    battery = next(item for item in artifact["components"] if item["component"] == "battery")
    assert set(battery["methods"]) == {
        "fixedInterval",
        "cumulativeDistance",
        "kaplanMeier",
    }
    assert battery["methods"]["fixedInterval"] == {
        key: value
        for key, value in result["methods"]["fixedInterval"]["components"][0].items()
        if key
        not in {
            "component",
            "sampleCount",
            "eventCount",
            "censoredCount",
            "minimumSampleCount",
            "minimumEventCount",
        }
    }
    assert battery["methods"]["kaplanMeier"]["curve"]["sourceSplit"] == "train"
    assert battery["methods"]["kaplanMeier"]["curve"]["points"][-1]["day"] == 30
    controller = next(item for item in artifact["components"] if item["component"] == "controller")
    assert controller["methods"]["kaplanMeier"]["status"] == "data_insufficient"
    assert "curve" not in controller["methods"]["kaplanMeier"]


def test_presentation_is_identity_free_and_self_hash_is_deterministic() -> None:
    dataset, result = _inputs()
    first = presentation.build_reliability_presentation(dataset, result)
    second = presentation.build_reliability_presentation(dataset, result)

    assert first == second
    assert presentation.reliability_presentation_hash(first) == first["artifactSha256"]
    rendered = json.dumps(first, sort_keys=True)
    assert all(
        forbidden not in rendered
        for forbidden in (
            "episodeId",
            "deviceGroupId",
            "outcomeAt",
            "riskResetEventId",
            "latitude",
            "longitude",
            "tenantId",
            "firebaseUid",
        )
    )


def test_console_snapshot_matches_generated_presentation_artifact() -> None:
    dataset, result = _inputs()
    generated = presentation.build_reliability_presentation(dataset, result)
    repository_root = Path(__file__).resolve().parents[3]
    snapshot_path = (
        repository_root / "apps" / "console" / "src" / "data" / "reliabilityComparisonArtifact.json"
    )

    assert json.loads(snapshot_path.read_text(encoding="utf-8")) == generated


def test_presentation_binds_dataset_and_baseline_result_hashes() -> None:
    dataset, result = _inputs()
    artifact = presentation.build_reliability_presentation(dataset, result)

    changed_dataset = copy.deepcopy(dataset)
    changed_dataset["episodes"][0]["cumulativeDistanceM"] += 1
    with pytest.raises(DatasetValidationError, match="datasetHash:mismatch"):
        presentation.validate_reliability_presentation(
            artifact, dataset=changed_dataset, result=result
        )

    changed_result = copy.deepcopy(result)
    changed_result["generatedAt"] = "2026-08-13T12:00:01Z"
    payload = {key: value for key, value in changed_result.items() if key != "resultSha256"}
    changed_result["resultSha256"] = hashlib.sha256(canonical_json(payload)).hexdigest()
    with pytest.raises(DatasetValidationError, match="baselineResultSha256:mismatch"):
        presentation.validate_reliability_presentation(
            artifact, dataset=dataset, result=changed_result
        )


def test_presentation_requires_dataset_and_result_together_for_provenance() -> None:
    dataset, result = _inputs()
    artifact = presentation.build_reliability_presentation(dataset, result)

    with pytest.raises(DatasetValidationError, match="dataset_and_result_required"):
        presentation.validate_reliability_presentation(artifact, dataset=dataset)
    with pytest.raises(DatasetValidationError, match="dataset_and_result_required"):
        presentation.validate_reliability_presentation(artifact, result=result)


def test_presentation_rejects_tampered_metrics_even_when_artifact_hash_is_recomputed() -> None:
    dataset, result = _inputs()
    artifact = presentation.build_reliability_presentation(dataset, result)
    tampered = copy.deepcopy(artifact)
    tampered["components"][0]["methods"]["fixedInterval"]["dueCount"] += 1
    _rehash(tampered)

    with pytest.raises(DatasetValidationError, match="method:binding"):
        presentation.validate_reliability_presentation(tampered, dataset=dataset, result=result)


def test_presentation_rejects_tampered_self_hash() -> None:
    dataset, result = _inputs()
    artifact = presentation.build_reliability_presentation(dataset, result)
    artifact["components"][0]["sampleCount"] += 1

    with pytest.raises(DatasetValidationError, match="artifactSha256:mismatch"):
        presentation.validate_reliability_presentation(artifact, dataset=dataset, result=result)


def test_test_labels_do_not_change_train_curve() -> None:
    dataset, result = _inputs()
    original = presentation.build_reliability_presentation(dataset, result)
    changed_dataset = copy.deepcopy(dataset)
    for row in changed_dataset["episodes"]:
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
    changed_result = evaluate_reliability_baselines(changed_dataset)
    changed = presentation.build_reliability_presentation(changed_dataset, changed_result)
    original_curves = {
        component["component"]: component["methods"]["kaplanMeier"].get("curve")
        for component in original["components"]
    }
    changed_curves = {
        component["component"]: component["methods"]["kaplanMeier"].get("curve")
        for component in changed["components"]
    }
    assert original_curves == changed_curves


def test_rehashed_intermediate_curve_tamper_is_bound_to_train_dataset() -> None:
    dataset, result = _inputs()
    artifact = presentation.build_reliability_presentation(dataset, result)
    battery = next(
        component for component in artifact["components"] if component["component"] == "battery"
    )
    points = battery["methods"]["kaplanMeier"]["curve"]["points"]
    points[1]["eventFreeProbability"] = points[0]["eventFreeProbability"]
    _rehash(artifact)
    with pytest.raises(DatasetValidationError, match="curve:dataset_binding"):
        presentation.validate_reliability_presentation(artifact, dataset=dataset, result=result)
