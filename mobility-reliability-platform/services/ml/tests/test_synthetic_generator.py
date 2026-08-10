from __future__ import annotations

import json
from datetime import datetime

import pytest

from mobility_ml.generate_r07_dataset import (
    DEFAULT_SEED,
    build_manifest,
    generate_dataset,
    write_dataset,
)
from mobility_ml.manifest import DATASET_VERSION, KNOWN_LABELS, validate_benchmark_dataset


def test_default_dataset_is_virtual_and_covers_four_classes() -> None:
    dataset = generate_dataset()
    assert dataset["seed"] == DEFAULT_SEED
    assert dataset["schemaVersion"] == DATASET_VERSION
    assert {trace["label"] for trace in dataset["traces"]} == set(KNOWN_LABELS)
    assert all(
        abs(sample["latitude"]) < 0.1 and abs(sample["longitude"]) < 0.1
        for trace in dataset["traces"]
        for sample in trace["batch"]["samples"]
    )
    assert all(trace["deviceProfile"] == "synthetic_generator" for trace in dataset["traces"])
    assert all(trace["sourceKind"] == "synthetic" for trace in dataset["traces"])
    assert all(trace["benchmarkEligible"] is True for trace in dataset["traces"])
    assert all(
        datetime.fromisoformat(trace["batch"]["sentAt"].replace("Z", "+00:00"))
        > datetime.fromisoformat(trace["batch"]["samples"][-1]["capturedAt"].replace("Z", "+00:00"))
        for trace in dataset["traces"]
    )


def test_manifest_is_deterministic_and_reports_counts() -> None:
    first = generate_dataset()
    second = generate_dataset()
    assert build_manifest(first) == build_manifest(second)
    manifest = build_manifest(first)
    assert manifest["traceCount"] == 48
    assert sum(trace["sampleCount"] for trace in manifest["traces"]) == 48 * 12
    assert manifest["splitCounts"] == {"train": 16, "validation": 16, "test": 16}
    assert manifest["labelCounts"] == {
        **{label: 12 for label in KNOWN_LABELS},
        "review_required": 0,
        "abstained": 0,
    }


def test_write_and_reload_preserves_dataset_hash(tmp_path) -> None:
    dataset_path, manifest_path = write_dataset(tmp_path)
    dataset = json.loads(dataset_path.read_text(encoding="utf-8"))
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    validate_benchmark_dataset(dataset)
    assert manifest["datasetSha256"] == build_manifest(dataset)["datasetSha256"]


def test_bad_trace_count_is_rejected() -> None:
    with pytest.raises(ValueError, match="divisible by 6"):
        generate_dataset(traces_per_label=7)
