from mobility_ml.generate_r07_dataset import build_manifest, generate_dataset
from mobility_ml.torch_candidate import canonical_result, train_and_evaluate


def test_torch_candidate_is_deterministic_and_uses_frozen_split() -> None:
    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    first = train_and_evaluate(dataset, manifest, epochs=120)
    second = train_and_evaluate(dataset, manifest, epochs=120)
    assert canonical_result(first) == canonical_result(second)
    assert first["datasetSha256"] == manifest["datasetSha256"]
    assert {key: value["count"] for key, value in first["splits"].items()} == {
        "train": 16,
        "validation": 16,
        "test": 16,
    }
    assert first["parameterCount"] == 292
    assert len(first["modelStateSha256"]) == 64
    assert all("latitude" not in prediction for prediction in first["predictions"])


def test_synthetic_candidate_does_not_authorize_deployment() -> None:
    dataset = generate_dataset()
    result = train_and_evaluate(dataset, build_manifest(dataset), epochs=120)
    assert result["evaluationScope"] == "synthetic_only"
    assert result["deploymentDecision"] == "defer"
    assert result["decisionReason"] in {
        "synthetic_only_no_improvement_over_rules",
        "field_validation_required",
    }
