"""Deterministic R07-C PyTorch candidate on frozen coordinate-free features.

This candidate is deliberately small. It proves a reproducible training and
evaluation boundary; synthetic scores must not be interpreted as field quality.
"""

from __future__ import annotations

import hashlib
import json
import random
from collections.abc import Mapping
from typing import Any

import torch
from torch import nn

from .features import extract_dataset_features, verify_feature_hash
from .manifest import KNOWN_LABELS, validate_manifest_against_dataset
from .rules_baseline import evaluate_frozen_dataset

MODEL_VERSION = "r07-tiny-feature-mlp.v1"
TRAINING_SEED = 20260813
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
        raw = tensor.detach().cpu().contiguous().view(torch.uint8).flatten().tolist()
        digest.update(bytes(raw))
    return digest.hexdigest()


def train_and_evaluate(
    dataset: Mapping[str, Any], manifest: Mapping[str, Any], *, epochs: int = 300
) -> dict[str, Any]:
    """Train only on frozen train rows and evaluate frozen validation/test rows."""
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


def canonical_result(result: Mapping[str, Any]) -> bytes:
    return json.dumps(
        result, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
    ).encode()
