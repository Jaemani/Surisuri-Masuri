from __future__ import annotations

import copy
import json
from pathlib import Path

import pytest

from mobility_ml.features import extract_dataset_features
from mobility_ml.generate_r07_dataset import build_manifest, generate_dataset
from mobility_ml.manifest import DatasetValidationError
from mobility_ml.torch_candidate import (
    FEATURE_KEYS,
    export_frozen_artifact,
    load_frozen_artifact,
    predict_frozen,
)


def _export(tmp_path: Path) -> tuple[dict, dict, dict, Path]:
    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    destination = tmp_path / "frozen"
    metadata = export_frozen_artifact(
        dataset,
        manifest,
        output_path=destination,
        epochs=120,
    )
    return dataset, manifest, metadata, destination


def test_export_load_and_prediction_are_frozen_and_coordinate_free(tmp_path: Path) -> None:
    dataset, manifest, metadata, destination = _export(tmp_path)
    artifact = load_frozen_artifact(destination)
    record = next(iter(extract_dataset_features(dataset, manifest).values()))
    result = predict_frozen(artifact, record)
    assert result["status"] == "predicted"
    assert result["predictedLabel"] in metadata["classLabels"]
    assert tuple(metadata["featureKeys"]) == FEATURE_KEYS
    assert metadata["trainingSourceKind"] == "synthetic"
    assert metadata["deploymentDecision"] == "defer"
    assert metadata["modelStateSha256"] == artifact.metadata["modelStateSha256"]
    assert all(not parameter.requires_grad for parameter in artifact.model.parameters())
    assert not {"latitude", "longitude", "expectedLabel", "split"}.intersection(result)


def test_export_is_deterministic_for_model_and_normalization(tmp_path: Path) -> None:
    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    first = export_frozen_artifact(dataset, manifest, output_path=tmp_path / "first", epochs=120)
    second = export_frozen_artifact(dataset, manifest, output_path=tmp_path / "second", epochs=120)
    assert first == second


def test_tampered_weights_or_metadata_fail_closed(tmp_path: Path) -> None:
    _dataset, _manifest, _metadata, destination = _export(tmp_path)
    weights_path = destination / "weights.pt"
    weights_path.write_bytes(weights_path.read_bytes() + b"tamper")
    with pytest.raises(DatasetValidationError, match="weightsSha256:mismatch"):
        load_frozen_artifact(destination)

    _dataset, _manifest, _metadata, destination = _export(tmp_path / "metadata")
    metadata_path = destination / "artifact.json"
    document = json.loads(metadata_path.read_text())
    document["featureKeys"] = list(reversed(document["featureKeys"]))
    metadata_path.write_text(json.dumps(document))
    with pytest.raises(DatasetValidationError, match="featureKeys:mismatch"):
        load_frozen_artifact(destination)


def test_review_feature_abstains_without_model_prediction(tmp_path: Path) -> None:
    dataset, manifest, _metadata, destination = _export(tmp_path)
    artifact = load_frozen_artifact(destination)
    record = next(iter(extract_dataset_features(dataset, manifest).values()))
    review = copy.deepcopy(record)
    review["extractionStatus"] = "review_required"
    review["reasonCode"] = "insufficient_samples"
    review["features"] = None
    from mobility_ml.features import _record_hash

    review["lineage"]["feature"]["featureSha256"] = _record_hash(review)
    result = predict_frozen(artifact, review)
    assert result["status"] == "abstain"
    assert result["predictedLabel"] == "unknown_review_required"
    assert result["probabilities"] is None
