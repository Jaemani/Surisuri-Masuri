"""Deterministic R07-C PyTorch candidate on frozen coordinate-free features.

This candidate is deliberately small. It proves a reproducible training and
evaluation boundary; synthetic scores must not be interpreted as field quality.
"""

from __future__ import annotations

import hashlib
import json
import math
import random
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import torch
from torch import nn

from .features import (
    FEATURE_EXTRACTOR_VERSION,
    FEATURE_SCHEMA_VERSION,
    extract_dataset_features,
    validate_feature_record_schema,
    verify_feature_hash,
)
from .field_features import (
    FIELD_FEATURE_VERSION,
    validate_field_feature_schema,
    verify_field_feature_hash,
)
from .manifest import (
    KNOWN_LABELS,
    DatasetValidationError,
    _validate_repository_schema,
    canonical_json,
    dataset_sha256,
    validate_manifest_against_dataset,
)
from .rules_baseline import evaluate_frozen_dataset

MODEL_VERSION = "r07-tiny-feature-mlp.v1"
MODEL_ARTIFACT_VERSION = "quality-model-artifact.v1"
TRAINING_SEED = 20260813
WEIGHTS_FILENAME = "weights.pt"
FEATURE_KEYS = (
    "pathLengthM",
    "displacementM",
    "meanStepDistanceM",
    "maxStepDistanceM",
    "reportedSpeedMeanMps",
    "reportedSpeedMaxMps",
    "reportedSpeedStdMps",
    "derivedSpeedMeanMps",
    "derivedSpeedMaxMps",
    "stationaryRatio",
    "accuracyMeanM",
    "accuracyMaxM",
    "optionalFieldMissingRatio",
)


class TinyFeatureMLP(nn.Module):
    def __init__(self) -> None:
        super().__init__()
        self.network = nn.Sequential(
            nn.Linear(len(FEATURE_KEYS), 16), nn.ReLU(), nn.Linear(16, len(KNOWN_LABELS))
        )

    def forward(self, values: torch.Tensor) -> torch.Tensor:
        return self.network(values)


@dataclass(frozen=True)
class FrozenInferenceArtifact:
    """Verified CPU model and training-only normalization for load-only inference."""

    metadata: Mapping[str, Any]
    model: TinyFeatureMLP
    mean: torch.Tensor
    std: torch.Tensor


def _seed() -> None:
    random.seed(TRAINING_SEED)
    torch.manual_seed(TRAINING_SEED)
    torch.use_deterministic_algorithms(True)
    torch.set_num_threads(1)


def _rows(
    dataset: Mapping[str, Any], records: Mapping[str, Mapping[str, Any]]
) -> list[dict[str, Any]]:
    rows = []
    for trace in sorted(dataset["traces"], key=lambda item: item["traceId"]):
        record = records[trace["traceId"]]
        if not verify_feature_hash(record) or not isinstance(record.get("features"), Mapping):
            raise ValueError("feature record unavailable or hash mismatch")
        rows.append(
            {
                "traceId": trace["traceId"],
                "split": trace["split"],
                "label": trace["label"],
                "values": [float(record["features"][key]) for key in FEATURE_KEYS],
            }
        )
    return rows


def _tensor(rows: list[dict[str, Any]]) -> tuple[torch.Tensor, torch.Tensor]:
    return torch.tensor([row["values"] for row in rows], dtype=torch.float32), torch.tensor(
        [KNOWN_LABELS.index(row["label"]) for row in rows], dtype=torch.long
    )


def _metrics(rows: list[dict[str, Any]], predictions: list[str]) -> dict[str, Any]:
    matrix = {actual: {predicted: 0 for predicted in KNOWN_LABELS} for actual in KNOWN_LABELS}
    for row, prediction in zip(rows, predictions, strict=True):
        matrix[row["label"]][prediction] += 1
    f1 = {}
    for label in KNOWN_LABELS:
        tp = matrix[label][label]
        fp = sum(matrix[actual][label] for actual in KNOWN_LABELS) - tp
        fn = sum(matrix[label].values()) - tp
        precision = tp / (tp + fp) if tp + fp else 0.0
        recall = tp / (tp + fn) if tp + fn else 0.0
        f1[label] = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    correct = sum(
        row["label"] == prediction for row, prediction in zip(rows, predictions, strict=True)
    )
    return {
        "count": len(rows),
        "accuracy": round(correct / len(rows), 12),
        "macroF1": round(sum(f1.values()) / len(f1), 12),
        "f1": {key: round(value, 12) for key, value in f1.items()},
        "confusionMatrix": matrix,
    }


def _state_hash(model: nn.Module) -> str:
    digest = hashlib.sha256()
    for name, tensor in sorted(model.state_dict().items()):
        digest.update(name.encode())
        digest.update(str(tensor.dtype).encode())
        digest.update(canonical_json(list(tensor.shape)))
        raw = tensor.detach().cpu().contiguous().view(torch.uint8).flatten().tolist()
        digest.update(bytes(raw))
    return digest.hexdigest()


def _fit_candidate(
    dataset: Mapping[str, Any], manifest: Mapping[str, Any], *, epochs: int
) -> tuple[
    TinyFeatureMLP,
    torch.Tensor,
    torch.Tensor,
    list[dict[str, Any]],
    dict[str, dict[str, Any]],
]:
    validate_manifest_against_dataset(manifest, dataset)
    _seed()
    records = extract_dataset_features(dataset, manifest)
    rows = _rows(dataset, records)
    train_rows = [row for row in rows if row["split"] == "train"]
    train_x, train_y = _tensor(train_rows)
    mean, std = train_x.mean(dim=0), train_x.std(dim=0, unbiased=False).clamp_min(1e-6)
    model = TinyFeatureMLP()
    optimizer = torch.optim.Adam(model.parameters(), lr=0.02, weight_decay=1e-4)
    for _ in range(epochs):
        optimizer.zero_grad()
        loss = nn.functional.cross_entropy(model((train_x - mean) / std), train_y)
        loss.backward()
        optimizer.step()
    model.eval()
    return model, mean, std, rows, records


def train_and_evaluate(
    dataset: Mapping[str, Any], manifest: Mapping[str, Any], *, epochs: int = 300
) -> dict[str, Any]:
    """Train only on frozen train rows and evaluate frozen validation/test rows."""
    if epochs < 1:
        raise ValueError("epochs must be positive")
    model, mean, std, rows, records = _fit_candidate(dataset, manifest, epochs=epochs)
    splits = {}
    predictions = []
    with torch.no_grad():
        for split in ("train", "validation", "test"):
            split_rows = [row for row in rows if row["split"] == split]
            values, _ = _tensor(split_rows)
            predicted = model((values - mean) / std).argmax(dim=1).tolist()
            labels = [KNOWN_LABELS[index] for index in predicted]
            splits[split] = _metrics(split_rows, labels)
            predictions.extend(
                {
                    "traceId": row["traceId"],
                    "split": split,
                    "expectedLabel": row["label"],
                    "predictedLabel": label,
                }
                for row, label in zip(split_rows, labels, strict=True)
            )
    rules = evaluate_frozen_dataset(dataset, manifest, records)
    test_delta = round(splits["test"]["macroF1"] - rules["splits"]["test"]["macroF1"], 12)
    return {
        "modelVersion": MODEL_VERSION,
        "evaluationScope": "synthetic_only",
        "sourceKind": "synthetic",
        "datasetSha256": manifest["datasetSha256"],
        "splitStrategy": manifest["splitStrategy"],
        "trainingSeed": TRAINING_SEED,
        "epochs": epochs,
        "featureKeys": list(FEATURE_KEYS),
        "parameterCount": sum(parameter.numel() for parameter in model.parameters()),
        "modelStateSha256": _state_hash(model),
        "splits": splits,
        "rulesBaselineTestMacroF1": rules["splits"]["test"]["macroF1"],
        "testMacroF1Delta": test_delta,
        "deploymentDecision": "defer",
        "decisionReason": "synthetic_only_no_improvement_over_rules"
        if test_delta <= 0
        else "field_validation_required",
        "predictions": predictions,
    }


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _without_artifact_hash(metadata: Mapping[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in metadata.items() if key != "artifactSha256"}


def _normalization_payload(mean: list[float], std: list[float]) -> dict[str, Any]:
    payload = {"mean": mean, "std": std}
    return {**payload, "normalizationSha256": hashlib.sha256(canonical_json(payload)).hexdigest()}


def _verify_artifact_hash(metadata: Mapping[str, Any]) -> bool:
    expected = metadata.get("artifactSha256")
    if not isinstance(expected, str):
        return False
    try:
        actual = hashlib.sha256(canonical_json(_without_artifact_hash(metadata))).hexdigest()
    except (TypeError, ValueError, OverflowError):
        return False
    return actual == expected


def _validate_artifact_metadata(metadata: Mapping[str, Any]) -> None:
    _validate_repository_schema(
        metadata,
        filename="quality-model-artifact.v1.schema.json",
        error_type=DatasetValidationError,
        label=MODEL_ARTIFACT_VERSION,
    )
    if tuple(metadata["featureKeys"]) != FEATURE_KEYS:
        raise DatasetValidationError("quality-model-artifact.v1 featureKeys:mismatch")
    if tuple(metadata["classLabels"]) != KNOWN_LABELS:
        raise DatasetValidationError("quality-model-artifact.v1 classLabels:mismatch")
    normalization = metadata["normalization"]
    expected_normalization = hashlib.sha256(
        canonical_json({"mean": normalization["mean"], "std": normalization["std"]})
    ).hexdigest()
    if normalization["normalizationSha256"] != expected_normalization:
        raise DatasetValidationError("quality-model-artifact.v1 normalizationSha256:mismatch")
    if not all(math.isfinite(float(value)) for value in normalization["mean"]):
        raise DatasetValidationError("quality-model-artifact.v1 normalization.mean:nonfinite")
    if not all(math.isfinite(float(value)) and float(value) > 0 for value in normalization["std"]):
        raise DatasetValidationError("quality-model-artifact.v1 normalization.std:nonpositive")
    if not _verify_artifact_hash(metadata):
        raise DatasetValidationError("quality-model-artifact.v1 artifactSha256:mismatch")


def export_frozen_artifact(
    dataset: Mapping[str, Any],
    manifest: Mapping[str, Any],
    *,
    output_path: Path | str,
    epochs: int = 300,
) -> dict[str, Any]:
    """Train once on the synthetic train split and write a frozen evaluation artifact."""

    if epochs < 1:
        raise ValueError("epochs must be positive")
    model, mean, std, _rows_unused, _records_unused = _fit_candidate(
        dataset, manifest, epochs=epochs
    )
    destination = Path(output_path).expanduser().resolve()
    destination.mkdir(parents=True, exist_ok=True)
    weights_path = destination / WEIGHTS_FILENAME
    metadata_path = destination / "artifact.json"
    if weights_path.exists() or metadata_path.exists():
        raise FileExistsError("frozen artifact destination is not empty")
    torch.save(model.state_dict(), weights_path)
    normalization = _normalization_payload(mean.tolist(), std.tolist())
    metadata: dict[str, Any] = {
        "schemaVersion": MODEL_ARTIFACT_VERSION,
        "modelVersion": MODEL_VERSION,
        "modelFamily": "TinyFeatureMLP",
        "trainingSourceKind": "synthetic",
        "trainingDatasetSha256": manifest["datasetSha256"],
        "trainingManifestSha256": dataset_sha256(manifest),
        "featureSchemaVersion": FEATURE_SCHEMA_VERSION,
        "featureExtractorVersion": FEATURE_EXTRACTOR_VERSION,
        "featureKeys": list(FEATURE_KEYS),
        "classLabels": list(KNOWN_LABELS),
        "normalization": normalization,
        "architecture": {
            "inputSize": len(FEATURE_KEYS),
            "hiddenSize": 16,
            "outputSize": len(KNOWN_LABELS),
        },
        "parameterCount": sum(parameter.numel() for parameter in model.parameters()),
        "trainingSeed": TRAINING_SEED,
        "epochs": epochs,
        "modelStateSha256": _state_hash(model),
        "weightsFilename": WEIGHTS_FILENAME,
        "weightsSha256": _file_sha256(weights_path),
        "deploymentDecision": "defer",
        "decisionReason": "synthetic_only_field_evaluation_required",
    }
    metadata["artifactSha256"] = hashlib.sha256(canonical_json(metadata)).hexdigest()
    _validate_artifact_metadata(metadata)
    metadata_path.write_bytes(canonical_json(metadata) + b"\n")
    return metadata


def load_frozen_artifact(artifact_path: Path | str) -> FrozenInferenceArtifact:
    """Load and verify an artifact without invoking a training or optimizer path."""

    source = Path(artifact_path).expanduser().resolve()
    metadata_path = source / "artifact.json"
    try:
        metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise DatasetValidationError("quality-model-artifact.v1 metadata:unavailable") from error
    if not isinstance(metadata, Mapping):
        raise DatasetValidationError("quality-model-artifact.v1 metadata:object")
    _validate_artifact_metadata(metadata)
    weights_path = source / metadata["weightsFilename"]
    if not weights_path.is_file() or _file_sha256(weights_path) != metadata["weightsSha256"]:
        raise DatasetValidationError("quality-model-artifact.v1 weightsSha256:mismatch")
    try:
        state = torch.load(weights_path, map_location="cpu", weights_only=True)
    except (OSError, RuntimeError, ValueError) as error:
        raise DatasetValidationError("quality-model-artifact.v1 weights:unavailable") from error
    if not isinstance(state, Mapping):
        raise DatasetValidationError("quality-model-artifact.v1 weights:state_dict")
    model = TinyFeatureMLP()
    expected = model.state_dict()
    if set(state) != set(expected):
        raise DatasetValidationError("quality-model-artifact.v1 weights:keys")
    for key, expected_tensor in expected.items():
        tensor = state[key]
        if not isinstance(tensor, torch.Tensor):
            raise DatasetValidationError("quality-model-artifact.v1 weights:tensor")
        if tensor.shape != expected_tensor.shape or tensor.dtype != expected_tensor.dtype:
            raise DatasetValidationError("quality-model-artifact.v1 weights:shape_dtype")
        if not torch.isfinite(tensor).all():
            raise DatasetValidationError("quality-model-artifact.v1 weights:nonfinite")
    model.load_state_dict(state, strict=True)
    if _state_hash(model) != metadata["modelStateSha256"]:
        raise DatasetValidationError("quality-model-artifact.v1 modelStateSha256:mismatch")
    model.eval()
    model.requires_grad_(False)
    mean = torch.tensor(metadata["normalization"]["mean"], dtype=torch.float32)
    std = torch.tensor(metadata["normalization"]["std"], dtype=torch.float32)
    return FrozenInferenceArtifact(metadata=metadata, model=model, mean=mean, std=std)


def _verified_features(record: Mapping[str, Any]) -> tuple[Mapping[str, Any] | None, str]:
    schema_version = record.get("schemaVersion")
    if schema_version == FEATURE_SCHEMA_VERSION:
        validate_feature_record_schema(record)
        valid_hash = verify_feature_hash(record)
    elif schema_version == FIELD_FEATURE_VERSION:
        validate_field_feature_schema(record)
        valid_hash = verify_field_feature_hash(record)
    else:
        raise DatasetValidationError("frozen inference feature schema:unsupported")
    if not valid_hash:
        raise DatasetValidationError("frozen inference feature hash:mismatch")
    if schema_version == FEATURE_SCHEMA_VERSION:
        feature_lineage = str(record["lineage"]["feature"]["featureSha256"])
    else:
        feature_lineage = str(record["featureSha256"])
    if record.get("extractionStatus") == "review_required":
        return None, feature_lineage
    features = record.get("features")
    if not isinstance(features, Mapping):
        raise DatasetValidationError("frozen inference features:unavailable")
    return features, feature_lineage


def predict_frozen(
    artifact: FrozenInferenceArtifact, feature_record: Mapping[str, Any]
) -> dict[str, Any]:
    """Run one verified, coordinate-free feature record through a frozen CPU model."""

    features, feature_lineage = _verified_features(feature_record)
    base = {
        "modelVersion": artifact.metadata["modelVersion"],
        "modelStateSha256": artifact.metadata["modelStateSha256"],
        "featureLineage": feature_lineage,
    }
    if features is None:
        return {
            **base,
            "status": "abstain",
            "predictedLabel": "unknown_review_required",
            "reasonCode": "feature_review_required",
            "probabilities": None,
        }
    try:
        values = torch.tensor(
            [float(features[key]) for key in artifact.metadata["featureKeys"]],
            dtype=torch.float32,
        )
    except (KeyError, TypeError, ValueError, OverflowError) as error:
        raise DatasetValidationError("frozen inference features:invalid") from error
    if not torch.isfinite(values).all():
        raise DatasetValidationError("frozen inference features:nonfinite")
    with torch.inference_mode():
        probabilities = torch.softmax(
            artifact.model((values - artifact.mean) / artifact.std), dim=0
        )
    index = int(probabilities.argmax().item())
    return {
        **base,
        "status": "predicted",
        "predictedLabel": artifact.metadata["classLabels"][index],
        "reasonCode": None,
        "probabilities": {
            label: round(float(probability), 12)
            for label, probability in zip(
                artifact.metadata["classLabels"], probabilities.tolist(), strict=True
            )
        },
    }


def canonical_result(result: Mapping[str, Any]) -> bytes:
    return json.dumps(
        result, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
    ).encode()
