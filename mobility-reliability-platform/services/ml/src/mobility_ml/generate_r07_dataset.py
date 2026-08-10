"""Deterministic synthetic telemetry generator for the August R07 milestone.

This module intentionally generates data around (0, 0), never real Seoul
coordinates.  It produces contract-valid ``telemetry-batch.v2`` batches and a
group/time holdout manifest for pipeline tests.  It does not claim model
quality or represent field data.
"""

from __future__ import annotations

import argparse
import random
import sys
import uuid
from collections.abc import Mapping
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

from .manifest import (
    CONTRACT_VERSION,
    DATASET_CREATED_AT,
    DATASET_ID,
    DATASET_VERSION,
    FEATURE_VERSION,
    GENERATOR_VERSION,
    KNOWN_LABELS,
    LABEL_VERSION,
    MANIFEST_VERSION,
    SPLIT_STRATEGY,
    canonical_json,
    dataset_sha256,
    validate_benchmark_dataset,
    validate_dataset_manifest,
    validate_manifest_against_dataset,
    validate_quality_label,
    validate_telemetry_batch_v2,
)

DEFAULT_SEED = 20260811
DEFAULT_TRACES_PER_LABEL = 12
SAMPLES_PER_TRACE = 12
UTC_START = datetime(2026, 8, 1, tzinfo=UTC)
UUID_NAMESPACE = uuid.UUID("8b3bf8de-68d9-5b3a-89b9-9d7c9e7b0e70")


def _uuid(seed: int, *parts: object) -> str:
    name = ":".join((str(seed), *(str(part) for part in parts)))
    return str(uuid.uuid5(UUID_NAMESPACE, name))


def _iso(value: datetime) -> str:
    return value.astimezone(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


def _random_for(seed: int, label: str, trace_index: int) -> random.Random:
    return random.Random(f"r07:{seed}:{label}:{trace_index}")


def _split_for(trace_index: int, traces_per_label: int) -> str:
    per_split = traces_per_label // 3
    if trace_index < per_split:
        return "train"
    if trace_index < per_split * 2:
        return "validation"
    return "test"


def _captured_at(trace_index: int, split: str, sample_index: int) -> datetime:
    # The large gaps make the temporal holdout property obvious in a chart.
    split_offset = {"train": 0, "validation": 20, "test": 40}[split]
    return UTC_START + timedelta(days=split_offset + trace_index % 4, seconds=sample_index * 5)


def _sample(
    *,
    seed: int,
    label: str,
    trace_index: int,
    sample_index: int,
    rng: random.Random,
    captured_at: datetime,
) -> dict[str, Any]:
    # Every coordinate is deliberately close to (0, 0).  This is a synthetic
    # virtual world and must never be presented as a Korean route.
    if label == "mobility_aid_likely":
        speed = max(0.15, 1.55 + rng.uniform(-0.18, 0.18))
        latitude = 0.00008 * sample_index + rng.uniform(-0.000005, 0.000005)
        longitude = 0.00011 * sample_index + rng.uniform(-0.000005, 0.000005)
        accuracy = 4.0 + rng.uniform(0, 4)
        activity = "wheeled"
        altitude = 0.1 + rng.uniform(-0.02, 0.02)
        heading = (42.0 + rng.uniform(-5, 5)) % 360
    elif label == "vehicle_likely":
        speed = max(4.0, 10.0 + rng.uniform(-1.0, 1.0))
        latitude = 0.0009 * sample_index + rng.uniform(-0.00002, 0.00002)
        longitude = 0.0012 * sample_index + rng.uniform(-0.00002, 0.00002)
        accuracy = 5.0 + rng.uniform(0, 5)
        activity = "motor_vehicle"
        altitude = 0.2 + rng.uniform(-0.05, 0.05)
        heading = (48.0 + rng.uniform(-4, 4)) % 360
    elif label == "stationary":
        speed = rng.uniform(0, 0.04)
        latitude = rng.uniform(-0.000003, 0.000003)
        longitude = rng.uniform(-0.000003, 0.000003)
        accuracy = 3.0 + rng.uniform(0, 3)
        activity = "stationary"
        altitude = 0.0 + rng.uniform(-0.02, 0.02)
        heading = None
    else:
        speed = None
        latitude = rng.uniform(-0.02, 0.02)
        longitude = rng.uniform(-0.02, 0.02)
        accuracy = 80.0 + rng.uniform(0, 60)
        activity = "unknown"
        altitude = None
        heading = None
    return {
        "clientSampleId": _uuid(seed, label, trace_index, "sample", sample_index),
        "sequence": sample_index,
        "capturedAt": _iso(captured_at),
        "latitude": round(latitude, 7),
        "longitude": round(longitude, 7),
        "horizontalAccuracyM": round(accuracy, 3),
        "altitudeM": None if altitude is None else round(altitude, 3),
        "speedMps": None if speed is None else round(speed, 3),
        "headingDegrees": None if heading is None else round(heading, 3),
        "activityHint": activity,
        "isMockLocation": False,
        "source": "phone_gps",
    }


def generate_trace(
    *, seed: int, label: str, trace_index: int, traces_per_label: int
) -> dict[str, Any]:
    """Generate one trace with a contract-valid mobile batch."""

    split = _split_for(trace_index, traces_per_label)
    group_index = trace_index // 2
    group_id = _uuid(seed, label, "scenario-group", group_index)
    trace_id = _uuid(seed, label, "trace", trace_index)
    label_id = _uuid(seed, label, "label", trace_index)
    rng = _random_for(seed, label, trace_index)
    samples = [
        _sample(
            seed=seed,
            label=label,
            trace_index=trace_index,
            sample_index=sample_index,
            rng=rng,
            captured_at=_captured_at(trace_index, split, sample_index),
        )
        for sample_index in range(SAMPLES_PER_TRACE)
    ]
    first_at = samples[0]["capturedAt"]
    batch = {
        "schemaVersion": CONTRACT_VERSION,
        "clientBatchId": _uuid(seed, label, "batch", trace_index),
        "tenantId": _uuid(seed, "tenant", "synthetic-r07"),
        "deviceId": _uuid(seed, label, "device", trace_index),
        "tripId": _uuid(seed, label, "trip", trace_index),
        "clientSessionId": _uuid(seed, label, "session", trace_index),
        "installationId": _uuid(seed, label, "installation", trace_index),
        "consentRevisionId": _uuid(seed, "consent", "synthetic-r07"),
        "sentAt": _iso(
            datetime.fromisoformat(samples[-1]["capturedAt"].replace("Z", "+00:00"))
            + timedelta(seconds=5)
        ),
        "samples": samples,
    }
    validate_telemetry_batch_v2(batch)
    return {
        "traceId": trace_id,
        "scenarioGroupId": group_id,
        "label": label,
        "labelId": label_id,
        "labelSchemaVersion": LABEL_VERSION,
        "split": split,
        "capturedAt": first_at,
        "deviceProfile": "synthetic_generator",
        "dataSource": "synthetic",
        "sourceKind": "synthetic",
        "benchmarkEligible": True,
        "batch": batch,
    }


def generate_dataset(
    seed: int = DEFAULT_SEED, traces_per_label: int = DEFAULT_TRACES_PER_LABEL
) -> dict[str, Any]:
    """Generate the complete deterministic R07 dataset.

    ``traces_per_label`` must be divisible by six: three splits and two traces
    per scenario group.  The default yields 48 traces and 576 GPS samples.
    """

    if traces_per_label < 6 or traces_per_label % 6:
        raise ValueError("traces_per_label must be at least 6 and divisible by 6")
    traces = [
        generate_trace(
            seed=seed,
            label=label,
            trace_index=trace_index,
            traces_per_label=traces_per_label,
        )
        for label in KNOWN_LABELS
        for trace_index in range(traces_per_label)
    ]
    labels = [
        {
            "schemaVersion": LABEL_VERSION,
            "labelId": trace["labelId"],
            "traceId": trace["traceId"],
            "scenarioGroupId": trace["scenarioGroupId"],
            "telemetryBatchId": trace["batch"]["clientBatchId"],
            "telemetrySchemaVersion": CONTRACT_VERSION,
            "labelState": "known",
            "label": trace["label"],
            "labelSource": "synthetic_generator",
            "confidence": 1.0,
            "labelGuideVersion": "quality-label-guide.r07.v1",
            "labeledAt": trace["capturedAt"],
        }
        for trace in traces
    ]
    for label in labels:
        validate_quality_label(label)
    dataset = {
        "schemaVersion": DATASET_VERSION,
        "generatorVersion": GENERATOR_VERSION,
        "featureVersion": FEATURE_VERSION,
        "splitStrategy": SPLIT_STRATEGY,
        "seed": seed,
        "source": "synthetic",
        "traces": traces,
        "labels": labels,
    }
    validate_benchmark_dataset(dataset)
    return dataset


def build_manifest(dataset: Mapping[str, Any]) -> dict[str, Any]:
    """Build deterministic metadata for a generated dataset."""

    traces = dataset.get("traces", [])
    split_counts = {
        split: sum(1 for trace in traces if trace.get("split") == split)
        for split in ("train", "validation", "test")
    }
    label_counts = {
        **{
            label: sum(1 for trace in traces if trace.get("label") == label)
            for label in KNOWN_LABELS
        },
        "review_required": 0,
        "abstained": 0,
    }
    manifest = {
        "schemaVersion": MANIFEST_VERSION,
        "datasetId": DATASET_ID,
        "datasetVersion": dataset.get("schemaVersion"),
        "telemetrySchemaVersion": CONTRACT_VERSION,
        "labelSchemaVersion": LABEL_VERSION,
        "featureSchemaVersion": dataset.get("featureVersion"),
        "generatorVersion": dataset.get("generatorVersion"),
        "splitStrategy": dataset.get("splitStrategy"),
        "seed": dataset.get("seed"),
        "sourceKind": "synthetic",
        "createdAt": DATASET_CREATED_AT,
        "datasetSha256": dataset_sha256(dataset),
        "traceCount": len(traces),
        "splitCounts": split_counts,
        "labelCounts": label_counts,
        "traces": [
            {
                "traceId": trace["traceId"],
                "telemetryBatchId": trace["batch"]["clientBatchId"],
                "scenarioGroupId": trace["scenarioGroupId"],
                "labelId": trace["labelId"],
                "sourceKind": "synthetic",
                "split": trace["split"],
                "capturedAt": trace["capturedAt"],
                "sampleCount": len(trace["batch"]["samples"]),
                "telemetrySha256": dataset_sha256(trace["batch"]),
                "benchmarkEligible": True,
            }
            for trace in traces
        ],
    }
    validate_dataset_manifest(manifest)
    validate_manifest_against_dataset(manifest, dataset)
    return manifest


def write_dataset(
    output_dir: Path | str,
    *,
    seed: int = DEFAULT_SEED,
    traces_per_label: int = DEFAULT_TRACES_PER_LABEL,
) -> tuple[Path, Path]:
    """Write ``dataset.json`` and ``manifest.json`` with stable formatting."""

    directory = Path(output_dir)
    directory.mkdir(parents=True, exist_ok=True)
    dataset = generate_dataset(seed=seed, traces_per_label=traces_per_label)
    manifest = build_manifest(dataset)
    dataset_path = directory / "dataset.json"
    manifest_path = directory / "manifest.json"
    dataset_path.write_bytes(canonical_json(dataset) + b"\n")
    manifest_path.write_bytes(canonical_json(manifest) + b"\n")
    return dataset_path, manifest_path


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="artifact directory")
    parser.add_argument("--seed", type=int, default=DEFAULT_SEED)
    parser.add_argument("--traces-per-label", type=int, default=DEFAULT_TRACES_PER_LABEL)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    dataset_path, manifest_path = write_dataset(
        args.output,
        seed=args.seed,
        traces_per_label=args.traces_per_label,
    )
    # Paths are safe metadata; no sample payload or coordinates are printed.
    print(f"generated {dataset_path} and {manifest_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
