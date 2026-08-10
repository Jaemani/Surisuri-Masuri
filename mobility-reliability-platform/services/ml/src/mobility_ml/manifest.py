"""Versioned dataset manifest and contract/split validation helpers.

Only metadata and validation failures are suitable for logs from this module.
In particular, validation errors never include latitude, longitude, names,
phone numbers, or raw payload values.
"""

from __future__ import annotations

import hashlib
import json
import os
import uuid
from collections.abc import Iterable, Mapping
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

KNOWN_LABELS: tuple[str, ...] = (
    "mobility_aid_likely",
    "vehicle_likely",
    "stationary",
    "gps_noise_or_insufficient",
)
ABSTAIN_LABEL = "unknown_review_required"
DATASET_VERSION = "quality-dataset.r07.synthetic.v1"
DATASET_ID = "quality-dataset.r07.synthetic"
DATASET_CREATED_AT = "2026-08-11T09:00:00Z"
MANIFEST_VERSION = "quality-dataset-manifest.v1"
LABEL_VERSION = "quality-label.v1"
FEATURE_VERSION = "quality-features.v1"
GENERATOR_VERSION = "r07-generator.v1"
SPLIT_STRATEGY = "group-time-holdout.v1"
CONTRACT_VERSION = "telemetry-batch.v2"


class ContractValidationError(ValueError):
    """Raised when a synthetic batch cannot satisfy the wire contract."""


class DatasetValidationError(ValueError):
    """Raised when a dataset cannot be used for a benchmark split."""


def canonical_json(value: Any) -> bytes:
    """Return the byte representation used for all artifact hashes."""

    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")


def dataset_sha256(dataset: Mapping[str, Any]) -> str:
    """Hash a dataset without relying on filesystem ordering or whitespace."""

    return hashlib.sha256(canonical_json(dataset)).hexdigest()


def _candidate_contract_paths(start: Path | None, filename: str) -> Iterable[Path]:
    configured = os.environ.get("MOBILITY_CONTRACTS_ROOT")
    if configured:
        root = Path(configured).expanduser()
        yield root / "schemas" / filename
        yield root / filename

    if start is None:
        start = Path(__file__).resolve()
    start = start.expanduser().resolve()
    if start.is_file():
        start = start.parent
    for parent in (start, *start.parents):
        yield parent / "packages" / "contracts" / "schemas" / filename
        yield parent / "schemas" / filename


def find_contract_schema(
    start: Path | str | None = None,
    filename: str = "telemetry-batch.v2.schema.json",
) -> Path:
    """Find a repository-owned JSON contract schema.

    The search is anchored to this package or an explicitly supplied path, so
    running the service from WSL, the repository root, or another current
    working directory resolves the same contract.  ``MOBILITY_CONTRACTS_ROOT``
    is available for isolated CI jobs.
    """

    path = Path(start) if start is not None else None
    for candidate in _candidate_contract_paths(path, filename):
        if candidate.is_file():
            return candidate
    raise FileNotFoundError(
        f"{filename} was not found; set MOBILITY_CONTRACTS_ROOT "
        "to packages/contracts or run inside the repository"
    )


def _schema_error_path(error: Any) -> str:
    path = ".".join(str(part) for part in getattr(error, "absolute_path", ()))
    return path or "$"


def _manual_batch_validation(batch: Mapping[str, Any]) -> list[str]:
    """Small dependency-free fallback for the checked-in JSON schema."""

    errors: list[str] = []
    required = (
        "schemaVersion",
        "clientBatchId",
        "tenantId",
        "deviceId",
        "tripId",
        "clientSessionId",
        "installationId",
        "consentRevisionId",
        "sentAt",
        "samples",
    )
    if not isinstance(batch, Mapping):
        return ["$:object"]
    for key in required:
        if key not in batch:
            errors.append(f"$.{key}:required")
    if set(batch) - set(required):
        errors.append("$:additionalProperties")
    if batch.get("schemaVersion") != CONTRACT_VERSION:
        errors.append("$.schemaVersion:const")
    for key in required[1:8]:
        value = batch.get(key)
        if not isinstance(value, str):
            errors.append(f"$.{key}:uuid")
        else:
            try:
                uuid.UUID(value)
            except (ValueError, AttributeError):
                errors.append(f"$.{key}:uuid")
    sent_at = batch.get("sentAt")
    if not isinstance(sent_at, str):
        errors.append("$.sentAt:date-time")
    else:
        try:
            datetime.fromisoformat(sent_at.replace("Z", "+00:00"))
        except ValueError:
            errors.append("$.sentAt:date-time")
    samples = batch.get("samples")
    if not isinstance(samples, list) or not 1 <= len(samples) <= 500:
        errors.append("$.samples:items")
        return errors
    sample_required = (
        "clientSampleId",
        "sequence",
        "capturedAt",
        "latitude",
        "longitude",
        "horizontalAccuracyM",
        "source",
    )
    for index, sample in enumerate(samples):
        prefix = f"$.samples.{index}"
        if not isinstance(sample, Mapping):
            errors.append(f"{prefix}:object")
            continue
        if set(sample) - {
            "clientSampleId",
            "sequence",
            "capturedAt",
            "latitude",
            "longitude",
            "horizontalAccuracyM",
            "altitudeM",
            "speedMps",
            "headingDegrees",
            "activityHint",
            "isMockLocation",
            "source",
        }:
            errors.append(f"{prefix}:additionalProperties")
        for key in sample_required:
            if key not in sample:
                errors.append(f"{prefix}.{key}:required")
        if not isinstance(sample.get("clientSampleId"), str):
            errors.append(f"{prefix}.clientSampleId:uuid")
        else:
            try:
                uuid.UUID(sample["clientSampleId"])
            except (ValueError, AttributeError):
                errors.append(f"{prefix}.clientSampleId:uuid")
        if (
            not isinstance(sample.get("sequence"), int)
            or isinstance(sample.get("sequence"), bool)
            or sample.get("sequence", -1) < 0
        ):
            errors.append(f"{prefix}.sequence:integer")
        captured_at = sample.get("capturedAt")
        if not isinstance(captured_at, str):
            errors.append(f"{prefix}.capturedAt:date-time")
        else:
            try:
                datetime.fromisoformat(captured_at.replace("Z", "+00:00"))
            except ValueError:
                errors.append(f"{prefix}.capturedAt:date-time")
        if sample.get("source") != "phone_gps":
            errors.append(f"{prefix}.source:const")
        for key in ("latitude", "longitude"):
            value = sample.get(key)
            if not isinstance(value, int | float):
                errors.append(f"{prefix}.{key}:number")
        if isinstance(sample.get("latitude"), int | float) and not -90 <= sample["latitude"] <= 90:
            errors.append(f"{prefix}.latitude:range")
        if (
            isinstance(sample.get("longitude"), int | float)
            and not -180 <= sample["longitude"] <= 180
        ):
            errors.append(f"{prefix}.longitude:range")
        accuracy = sample.get("horizontalAccuracyM")
        if accuracy is not None and (
            not isinstance(accuracy, int | float) or isinstance(accuracy, bool) or accuracy < 0
        ):
            errors.append(f"{prefix}.horizontalAccuracyM:number")
        speed = sample.get("speedMps")
        if speed is not None and (
            not isinstance(speed, int | float) or isinstance(speed, bool) or speed < 0
        ):
            errors.append(f"{prefix}.speedMps:number")
        heading = sample.get("headingDegrees")
        if heading is not None and (
            not isinstance(heading, int | float)
            or isinstance(heading, bool)
            or not 0 <= heading < 360
        ):
            errors.append(f"{prefix}.headingDegrees:range")
        if sample.get("activityHint") not in {
            None,
            "unknown",
            "stationary",
            "walking",
            "wheeled",
            "motor_vehicle",
        }:
            errors.append(f"{prefix}.activityHint:enum")
    return errors


def validate_telemetry_batch_v2(
    batch: Mapping[str, Any],
    schema_path: Path | str | None = None,
) -> None:
    """Validate a batch against the repository's v2 JSON Schema.

    The repository schema is the only source of truth. Error text contains
    paths and keywords only; raw values are intentionally omitted to prevent
    accidental coordinate leakage.
    """

    errors: list[str]
    if schema_path is None:
        schema = find_contract_schema()
    else:
        schema = Path(schema_path).expanduser().resolve()
    try:
        schema_document = json.loads(schema.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ContractValidationError("contract schema unavailable") from error

    try:
        import jsonschema  # type: ignore[import-not-found]
    except ImportError as error:
        raise ContractValidationError("telemetry-batch.v2 validator unavailable") from error
    else:
        validator = jsonschema.Draft202012Validator(
            schema_document,
            format_checker=jsonschema.FormatChecker(),
        )
        errors = [
            f"{_schema_error_path(error)}:{error.validator}"
            for error in sorted(validator.iter_errors(batch), key=_schema_error_path)
        ]
    if errors:
        raise ContractValidationError("telemetry-batch.v2 invalid: " + ", ".join(errors[:12]))


def _validate_repository_schema(
    value: Mapping[str, Any],
    *,
    filename: str,
    error_type: type[ValueError],
    label: str,
) -> None:
    """Validate a metadata object without including raw values in failures."""

    try:
        schema = find_contract_schema(filename=filename)
        schema_document = json.loads(schema.read_text(encoding="utf-8"))
    except (FileNotFoundError, OSError, json.JSONDecodeError) as error:
        raise error_type(f"{label} schema unavailable") from error
    try:
        import jsonschema  # type: ignore[import-not-found]
    except ImportError as error:
        # jsonschema is a core dependency. A broken environment must not
        # silently turn a contract check into a pass.
        raise error_type(f"{label} validator unavailable") from error
    errors = [
        f"{_schema_error_path(error)}:{error.validator}"
        for error in sorted(
            jsonschema.Draft202012Validator(
                schema_document,
                format_checker=jsonschema.FormatChecker(),
            ).iter_errors(value),
            key=_schema_error_path,
        )
    ]
    if errors:
        raise error_type(f"{label} invalid: " + ", ".join(errors[:12]))


def validate_quality_label(label: Mapping[str, Any]) -> None:
    """Validate one quality-label.v1 metadata object."""

    _validate_repository_schema(
        label,
        filename="quality-label.v1.schema.json",
        error_type=DatasetValidationError,
        label="quality-label.v1",
    )


def validate_dataset_manifest(manifest: Mapping[str, Any]) -> None:
    """Validate the checked-in quality-dataset-manifest.v1 contract."""

    _validate_repository_schema(
        manifest,
        filename="quality-dataset-manifest.v1.schema.json",
        error_type=DatasetValidationError,
        label="quality-dataset-manifest.v1",
    )


def _trace_samples(trace: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    batch = trace.get("batch")
    if not isinstance(batch, Mapping):
        return []
    samples = batch.get("samples")
    if not isinstance(samples, list):
        return []
    return [sample for sample in samples if isinstance(sample, Mapping)]


def _parse_timestamp(value: Any) -> datetime | None:
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        return None
    return parsed.astimezone(UTC)


def _trace_time(trace: Mapping[str, Any]) -> datetime | None:
    return _parse_timestamp(trace.get("capturedAt"))


def _trace_is_developer_device(trace: Mapping[str, Any]) -> bool:
    # These are explicit provenance fields, not free-text scanning.  A UUID or
    # a route value can never accidentally be rejected as a developer device.
    if trace.get("sourceKind") == "developer_device":
        return True
    for key in ("deviceProfile", "deviceType", "dataSource"):
        if trace.get(key) == "developer_device":
            return True
    provenance = trace.get("provenance")
    return isinstance(provenance, Mapping) and provenance.get("deviceProfile") == "developer_device"


def check_group_time_holdout(
    traces: Iterable[Mapping[str, Any]],
    known_labels: Iterable[str] = KNOWN_LABELS,
) -> list[str]:
    """Return safe, value-free errors for group and temporal split checks."""

    errors: list[str] = []
    expected = set(known_labels)
    group_splits: dict[str, set[str]] = {}
    split_times: dict[str, list[datetime]] = {"train": [], "validation": [], "test": []}
    split_labels: dict[str, set[str]] = {"train": set(), "validation": set(), "test": set()}
    for index, trace in enumerate(traces):
        if not isinstance(trace, Mapping):
            errors.append(f"trace[{index}]:object")
            continue
        label = trace.get("label")
        split = trace.get("split")
        group = trace.get("scenarioGroupId")
        if label not in expected:
            errors.append(f"trace[{index}].label:known")
        if split not in split_times:
            errors.append(f"trace[{index}].split:allowed")
        if not isinstance(group, str) or not group:
            errors.append(f"trace[{index}].scenarioGroupId:required")
        elif split in split_times:
            group_splits.setdefault(group, set()).add(split)
        if split in split_times and label in expected:
            split_labels[split].add(label)
        if _trace_is_developer_device(trace):
            errors.append(f"trace[{index}].deviceProfile:benchmark_forbidden")
        batch = trace.get("batch")
        if not isinstance(batch, Mapping):
            errors.append(f"trace[{index}].batch:required")
        else:
            try:
                validate_telemetry_batch_v2(batch)
            except ContractValidationError:
                errors.append(f"trace[{index}].batch:telemetry-batch.v2")
        trace_time = _trace_time(trace)
        samples = _trace_samples(trace)
        sample_times = [_parse_timestamp(sample.get("capturedAt")) for sample in samples]
        if trace_time is None:
            errors.append(f"trace[{index}].capturedAt:date-time")
        if not samples or any(timestamp is None for timestamp in sample_times):
            errors.append(f"trace[{index}].batch.samples.capturedAt:date-time")
        else:
            valid_sample_times = [timestamp for timestamp in sample_times if timestamp is not None]
            if valid_sample_times != sorted(valid_sample_times):
                errors.append(f"trace[{index}].batch.samples.capturedAt:order")
            if trace_time != valid_sample_times[0]:
                errors.append(f"trace[{index}].capturedAt:sample_linkage")
            sent_at = _parse_timestamp(batch.get("sentAt")) if isinstance(batch, Mapping) else None
            if sent_at is None or sent_at <= valid_sample_times[-1]:
                errors.append(f"trace[{index}].batch.sentAt:chronology")
            if split in split_times:
                split_times[split].extend(valid_sample_times)

    for _group, splits in group_splits.items():
        if len(splits) > 1:
            errors.append("scenarioGroupId:split_leakage")
    for split in ("validation", "test"):
        if split_labels[split] != expected:
            errors.append(f"{split}:known_class_coverage")
    if not split_times["train"] or not split_times["validation"] or not split_times["test"]:
        errors.append("split:missing")
    else:
        if max(split_times["train"]) >= min(split_times["validation"]):
            errors.append("split:train_validation_time_leakage")
        if max(split_times["validation"]) >= min(split_times["test"]):
            errors.append("split:validation_test_time_leakage")
    return errors


def validate_group_time_holdout(
    traces: Iterable[Mapping[str, Any]],
    known_labels: Iterable[str] = KNOWN_LABELS,
) -> None:
    """Raise if groups, dates, labels, or provenance make a benchmark unsafe."""

    errors = check_group_time_holdout(traces, known_labels)
    if errors:
        raise DatasetValidationError("unsafe group/time holdout: " + ", ".join(errors[:16]))


def validate_benchmark_dataset(dataset: Mapping[str, Any]) -> None:
    """Validate dataset metadata and all traces before a benchmark load."""

    if not isinstance(dataset, Mapping):
        raise DatasetValidationError("dataset:object")
    if dataset.get("schemaVersion") != DATASET_VERSION:
        raise DatasetValidationError("dataset schemaVersion is not the R07 synthetic version")
    if dataset.get("source") != "synthetic":
        raise DatasetValidationError("benchmark loader accepts synthetic data only")
    expected_versions = {
        "generatorVersion": GENERATOR_VERSION,
        "featureVersion": FEATURE_VERSION,
        "splitStrategy": SPLIT_STRATEGY,
    }
    if any(dataset.get(key) != value for key, value in expected_versions.items()):
        raise DatasetValidationError("dataset provenance version:mismatch")
    seed = dataset.get("seed")
    if not isinstance(seed, int) or isinstance(seed, bool) or seed < 0:
        raise DatasetValidationError("dataset seed:invalid")
    traces = dataset.get("traces")
    if not isinstance(traces, list) or not traces:
        raise DatasetValidationError("dataset traces are missing")
    labels = dataset.get("labels")
    if not isinstance(labels, list) or len(labels) != len(traces):
        raise DatasetValidationError("dataset labels do not match trace count")
    label_by_id: dict[str, Mapping[str, Any]] = {}
    for label in labels:
        if not isinstance(label, Mapping):
            raise DatasetValidationError("dataset label is not an object")
        validate_quality_label(label)
        label_id = label.get("labelId")
        if not isinstance(label_id, str) or label_id in label_by_id:
            raise DatasetValidationError("dataset label identity is missing or duplicated")
        label_by_id[label_id] = label
    seen_trace_ids: set[str] = set()
    seen_batch_ids: set[str] = set()
    for index, trace in enumerate(traces):
        if not isinstance(trace, Mapping):
            raise DatasetValidationError(f"trace[{index}]:object")
        if trace.get("sourceKind") != "synthetic" or trace.get("benchmarkEligible") is not True:
            raise DatasetValidationError(f"trace[{index}].sourceKind:benchmark_forbidden")
        label = label_by_id.get(trace.get("labelId"))
        batch = trace.get("batch")
        if label is None or not isinstance(batch, Mapping):
            raise DatasetValidationError(f"trace[{index}]:label_batch_linkage")
        trace_id = trace.get("traceId")
        batch_id = batch.get("clientBatchId")
        try:
            parsed_trace_id = str(uuid.UUID(trace_id)) if isinstance(trace_id, str) else None
        except ValueError:
            parsed_trace_id = None
        if parsed_trace_id != trace_id or trace_id in seen_trace_ids:
            raise DatasetValidationError(f"trace[{index}].traceId:missing_or_duplicated")
        if not isinstance(batch_id, str) or batch_id in seen_batch_ids:
            raise DatasetValidationError(f"trace[{index}].telemetryBatchId:duplicated")
        seen_trace_ids.add(trace_id)
        seen_batch_ids.add(batch_id)
        expected_linkage = (
            ("traceId", trace.get("traceId")),
            ("scenarioGroupId", trace.get("scenarioGroupId")),
            ("telemetryBatchId", batch.get("clientBatchId")),
            ("label", trace.get("label")),
        )
        if any(label.get(key) != expected for key, expected in expected_linkage):
            raise DatasetValidationError(f"trace[{index}]:label_batch_linkage")
    validate_group_time_holdout(traces)


def validate_manifest_against_dataset(
    manifest: Mapping[str, Any], dataset: Mapping[str, Any]
) -> None:
    """Fail closed when a manifest no longer describes the supplied dataset."""

    validate_benchmark_dataset(dataset)
    validate_dataset_manifest(manifest)
    traces = dataset["traces"]
    manifest_traces = manifest["traces"]
    expected_root = {
        "datasetId": DATASET_ID,
        "datasetVersion": dataset.get("schemaVersion"),
        "telemetrySchemaVersion": CONTRACT_VERSION,
        "labelSchemaVersion": LABEL_VERSION,
        "featureSchemaVersion": dataset.get("featureVersion"),
        "generatorVersion": GENERATOR_VERSION,
        "splitStrategy": SPLIT_STRATEGY,
        "seed": dataset.get("seed"),
        "sourceKind": "synthetic",
        "createdAt": DATASET_CREATED_AT,
    }
    if any(manifest.get(key) != value for key, value in expected_root.items()):
        raise DatasetValidationError("manifest provenance:mismatch")
    if manifest.get("datasetSha256") != dataset_sha256(dataset):
        raise DatasetValidationError("manifest datasetSha256:mismatch")
    expected_split_counts = {
        split: sum(1 for trace in traces if trace.get("split") == split)
        for split in ("train", "validation", "test")
    }
    expected_label_counts = {
        **{
            label: sum(1 for trace in traces if trace.get("label") == label)
            for label in KNOWN_LABELS
        },
        "review_required": 0,
        "abstained": 0,
    }
    if manifest.get("traceCount") != len(traces):
        raise DatasetValidationError("manifest traceCount:mismatch")
    if manifest.get("splitCounts") != expected_split_counts:
        raise DatasetValidationError("manifest splitCounts:mismatch")
    if manifest.get("labelCounts") != expected_label_counts:
        raise DatasetValidationError("manifest labelCounts:mismatch")
    if len(manifest_traces) != len(traces):
        raise DatasetValidationError("manifest traces:mismatch")
    expected_by_trace_id = {
        trace["traceId"]: {
            "telemetryBatchId": trace["batch"]["clientBatchId"],
            "scenarioGroupId": trace["scenarioGroupId"],
            "labelId": trace["labelId"],
            "split": trace["split"],
            "capturedAt": trace["capturedAt"],
            "sampleCount": len(trace["batch"]["samples"]),
            "telemetrySha256": dataset_sha256(trace["batch"]),
            "sourceKind": "synthetic",
            "benchmarkEligible": True,
        }
        for trace in traces
    }
    seen: set[str] = set()
    for entry in manifest_traces:
        trace_id = entry.get("traceId")
        expected = expected_by_trace_id.get(trace_id)
        if expected is None or trace_id in seen:
            raise DatasetValidationError("manifest trace identity:mismatch")
        seen.add(trace_id)
        if any(entry.get(key) != value for key, value in expected.items()):
            raise DatasetValidationError("manifest trace metadata:mismatch")
