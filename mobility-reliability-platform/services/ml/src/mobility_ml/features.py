"""Deterministic, coordinate-free feature extraction for R07-B2.

The calculation boundary accepts one ``telemetry-batch.v2`` object only. Labels
and split metadata are joined by ``extract_dataset_features`` after calculation
and are never read by the numeric extractor. Raw coordinates are used only for
transient haversine arithmetic; they are not returned or logged.
"""

from __future__ import annotations

import hashlib
import json
import math
import uuid
from collections.abc import Mapping, Sequence
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .manifest import (
    CONTRACT_VERSION,
    DATASET_ID,
    DATASET_VERSION,
    FEATURE_VERSION,
    ContractValidationError,
    DatasetValidationError,
    canonical_json,
    find_contract_schema,
    validate_manifest_against_dataset,
    validate_telemetry_batch_v2,
)
from .manifest import (
    dataset_sha256 as hash_json,
)

FEATURE_SCHEMA_VERSION = FEATURE_VERSION
FEATURE_EXTRACTOR_VERSION = "r07-feature-extractor.v1"
FEATURE_TRACE_VERSION = "quality-trace.v1"
FEATURE_SCHEMA_FILENAME = "quality-features.v1.schema.json"
FEATURE_STATUS_READY = "ok"
FEATURE_STATUS_REVIEW = "review_required"
FEATURE_NAMESPACE = uuid.UUID("4e91f2c0-06e2-5e4f-9723-b4e5e5af2b85")
EARTH_RADIUS_M = 6_371_008.8


class FeatureContractUnavailable(RuntimeError):
    """Raised when an optional repository feature schema cannot be found."""


class FeatureLeakageError(ValueError):
    """Raised when labels or split metadata enter a feature-only boundary."""


def _finite(value: Any) -> bool:
    return (
        isinstance(value, int | float)
        and not isinstance(value, bool)
        and math.isfinite(float(value))
    )


def _timestamp(value: Any) -> datetime | None:
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        return None
    return parsed.astimezone(UTC)


def _round(value: float) -> float:
    return round(float(value), 9)


def _mean(values: Sequence[float]) -> float:
    return math.fsum(values) / len(values) if values else 0.0


def _population_std(values: Sequence[float]) -> float:
    if not values:
        return 0.0
    mean = _mean(values)
    return math.sqrt(math.fsum((value - mean) ** 2 for value in values) / len(values))


def _percentile(values: Sequence[float], percentile: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    position = (len(ordered) - 1) * percentile
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    fraction = position - lower
    return ordered[lower] + (ordered[upper] - ordered[lower]) * fraction


def _haversine_m(
    latitude_a: float, longitude_a: float, latitude_b: float, longitude_b: float
) -> float:
    """Calculate a distance without retaining either coordinate."""

    lat_a = math.radians(latitude_a)
    lat_b = math.radians(latitude_b)
    delta_lat = lat_b - lat_a
    delta_lon = math.radians(longitude_b - longitude_a)
    hav = (
        math.sin(delta_lat / 2) ** 2
        + math.cos(lat_a) * math.cos(lat_b) * math.sin(delta_lon / 2) ** 2
    )
    return 2 * EARTH_RADIUS_M * math.asin(min(1.0, max(0.0, hav)) ** 0.5)


def _record_hash(record: Mapping[str, Any]) -> str | None:
    """Hash the complete feature record except the digest field itself."""

    lineage = record.get("lineage")
    if not isinstance(lineage, Mapping):
        return None
    feature_lineage = lineage.get("feature")
    if not isinstance(feature_lineage, Mapping):
        return None
    lineage_without_digest = {
        **lineage,
        "feature": {key: value for key, value in feature_lineage.items() if key != "featureSha256"},
    }
    payload = {**record, "lineage": lineage_without_digest}
    return hashlib.sha256(canonical_json(payload)).hexdigest()


def _uuid_for(*parts: object) -> str:
    return str(uuid.uuid5(FEATURE_NAMESPACE, ":".join(str(part) for part in parts)))


def _batch_hash(batch: Mapping[str, Any]) -> str:
    try:
        return hash_json(batch)
    except (TypeError, ValueError, OverflowError):
        # Invalid JSON numbers (for example NaN) must still produce a
        # coordinate-free lineage digest. Never serialize the malformed
        # payload into a review record or an exception message.
        safe_identity = {
            "schemaVersion": batch.get("schemaVersion"),
            "clientBatchId": batch.get("clientBatchId"),
            "invalidPayload": True,
        }
        return hashlib.sha256(canonical_json(safe_identity)).hexdigest()


def _trace_hash(trace_id: str, batch: Mapping[str, Any]) -> str:
    # Trace lineage intentionally excludes label and split. The batch digest
    # identifies the raw trace without exposing its coordinate payload.
    return hash_json(
        {
            "traceId": trace_id,
            "telemetryBatchId": batch.get("clientBatchId"),
            "batchSha256": _batch_hash(batch),
        }
    )


def _feature_lineage(
    *,
    batch: Mapping[str, Any],
    trace_id: str,
    dataset_hash: str,
    split: str,
    source_kind: str,
    benchmark_eligible: bool,
) -> dict[str, Any]:
    return {
        "trace": {
            "traceId": trace_id,
            "traceVersion": FEATURE_TRACE_VERSION,
            "traceSha256": _trace_hash(trace_id, batch),
        },
        "telemetryBatch": {
            "batchId": batch["clientBatchId"],
            "schemaVersion": CONTRACT_VERSION,
            "batchSha256": _batch_hash(batch),
        },
        "dataset": {
            "datasetId": DATASET_ID,
            "datasetVersion": DATASET_VERSION,
            "datasetSha256": dataset_hash,
            "split": split,
            "sourceKind": source_kind,
            "benchmarkEligible": benchmark_eligible,
        },
        "feature": {
            "featureVersion": FEATURE_SCHEMA_VERSION,
            "extractorVersion": FEATURE_EXTRACTOR_VERSION,
        },
    }


def _review_reason(reasons: Sequence[str]) -> str:
    priority = (
        "developer_device_not_benchmarkable",
        "contract_mismatch",
        "non_finite_value",
        "out_of_range",
        "invalid_or_missing_accuracy",
        "missing_timestamps",
        "non_monotonic_time",
        "insufficient_samples",
        "unsupported_source",
        "extractor_error",
    )
    return next((value for value in priority if value in reasons), "extractor_error")


def _review_core(reason: str) -> dict[str, Any]:
    return {
        "extractionStatus": FEATURE_STATUS_REVIEW,
        "reasonCode": reason,
        "features": None,
    }


def _validate_sample_values(samples: Sequence[Mapping[str, Any]]) -> list[str]:
    reasons: list[str] = []
    previous_time: datetime | None = None
    for index, sample in enumerate(samples):
        if sample.get("sequence") != index or isinstance(sample.get("sequence"), bool):
            reasons.append("extractor_error")
        captured_at = _timestamp(sample.get("capturedAt"))
        if captured_at is None:
            reasons.append("missing_timestamps")
        elif previous_time is not None and captured_at <= previous_time:
            reasons.append("non_monotonic_time")
        previous_time = captured_at

        latitude = sample.get("latitude")
        longitude = sample.get("longitude")
        if not _finite(latitude) or not _finite(longitude):
            reasons.append("non_finite_value")
        elif not -90 <= float(latitude) <= 90 or not -180 <= float(longitude) <= 180:
            reasons.append("out_of_range")

        accuracy = sample.get("horizontalAccuracyM")
        if accuracy is not None and (not _finite(accuracy) or float(accuracy) < 0):
            reasons.append("invalid_or_missing_accuracy")
        speed = sample.get("speedMps")
        if speed is not None and (not _finite(speed) or float(speed) < 0):
            reasons.append("out_of_range" if _finite(speed) else "non_finite_value")
        heading = sample.get("headingDegrees")
        if heading is not None and (not _finite(heading) or not 0 <= float(heading) < 360):
            reasons.append("out_of_range" if _finite(heading) else "non_finite_value")
        altitude = sample.get("altitudeM")
        if altitude is not None and not _finite(altitude):
            reasons.append("non_finite_value")
    if not any(sample.get("horizontalAccuracyM") is not None for sample in samples):
        reasons.append("invalid_or_missing_accuracy")
    return reasons


def _calculate_numeric_features(
    samples: Sequence[Mapping[str, Any]], sent_at: datetime
) -> dict[str, float | int]:
    times = [_timestamp(sample["capturedAt"]) for sample in samples]
    if any(value is None for value in times):
        raise ValueError("validated samples must have timestamps")
    timestamps = [value for value in times if value is not None]
    intervals = [
        (right - left).total_seconds()
        for left, right in zip(timestamps, timestamps[1:], strict=False)
    ]
    latitudes = [float(sample["latitude"]) for sample in samples]
    longitudes = [float(sample["longitude"]) for sample in samples]
    distances = [
        _haversine_m(
            latitudes[index],
            longitudes[index],
            latitudes[index + 1],
            longitudes[index + 1],
        )
        for index in range(len(samples) - 1)
    ]
    derived_speeds = [
        distance / interval
        for distance, interval in zip(distances, intervals, strict=False)
        if interval > 0
    ]
    reported_speeds = [
        float(sample["speedMps"]) for sample in samples if sample.get("speedMps") is not None
    ]
    reported_accelerations = [
        (float(right["speedMps"]) - float(left["speedMps"])) / interval
        for left, right, interval in zip(samples, samples[1:], intervals, strict=False)
        if left.get("speedMps") is not None and right.get("speedMps") is not None and interval > 0
    ]
    headings = [sample.get("headingDegrees") for sample in samples]
    heading_changes = [
        abs(((float(right) - float(left) + 180.0) % 360.0) - 180.0)
        for left, right in zip(headings, headings[1:], strict=False)
        if left is not None and right is not None
    ]
    accuracies = [
        float(sample["horizontalAccuracyM"])
        for sample in samples
        if sample.get("horizontalAccuracyM") is not None
    ]
    if not accuracies:
        raise ValueError("no accuracy values")
    altitude_missing = sum(sample.get("altitudeM") is None for sample in samples)
    speed_missing = sum(sample.get("speedMps") is None for sample in samples)
    heading_missing = sum(sample.get("headingDegrees") is None for sample in samples)
    accuracy_missing = sum(sample.get("horizontalAccuracyM") is None for sample in samples)
    optional_missing = altitude_missing + speed_missing + heading_missing + accuracy_missing
    stationary_count = sum(
        sample.get("activityHint") == "stationary"
        or (sample.get("speedMps") is not None and float(sample["speedMps"]) <= 0.3)
        for sample in samples
    )
    duration = (timestamps[-1] - timestamps[0]).total_seconds()
    return {
        "sampleCount": len(samples),
        "observedDurationS": _round(duration),
        "pathLengthM": _round(math.fsum(distances)),
        "displacementM": _round(
            _haversine_m(latitudes[0], longitudes[0], latitudes[-1], longitudes[-1])
        ),
        "meanStepDistanceM": _round(_mean(distances)),
        "maxStepDistanceM": _round(max(distances, default=0.0)),
        "meanSampleIntervalS": _round(_mean(intervals)),
        "maxSampleIntervalS": _round(max(intervals, default=0.0)),
        "reportedSpeedMeanMps": _round(_mean(reported_speeds)),
        "reportedSpeedMaxMps": _round(max(reported_speeds, default=0.0)),
        "reportedSpeedStdMps": _round(_population_std(reported_speeds)),
        "derivedSpeedMeanMps": _round(_mean(derived_speeds)),
        "derivedSpeedMaxMps": _round(max(derived_speeds, default=0.0)),
        "reportedAccelerationMeanAbsMps2": _round(
            _mean([abs(value) for value in reported_accelerations])
        ),
        "reportedAccelerationMaxAbsMps2": _round(
            max((abs(value) for value in reported_accelerations), default=0.0)
        ),
        "headingChangeMeanDeg": _round(_mean(heading_changes)),
        "headingChangeMaxDeg": _round(max(heading_changes, default=0.0)),
        "headingChangeTotalDeg": _round(math.fsum(heading_changes)),
        "headingChangeCount": len(heading_changes),
        "stationaryRatio": _round(stationary_count / len(samples)),
        "accuracyMeanM": _round(_mean(accuracies)),
        "accuracyMaxM": _round(max(accuracies, default=0.0)),
        "accuracyMissingRatio": _round(accuracy_missing / len(samples)),
        "speedMissingRatio": _round(speed_missing / len(samples)),
        "headingMissingRatio": _round(heading_missing / len(samples)),
        "altitudeMissingRatio": _round(altitude_missing / len(samples)),
        "optionalFieldMissingRatio": _round(optional_missing / (len(samples) * 4)),
        "sentAfterLastSampleS": _round((sent_at - timestamps[-1]).total_seconds()),
    }


def calculate_feature_core(batch: Mapping[str, Any]) -> dict[str, Any]:
    """Calculate status/reason/numeric features from a batch only.

    This is the strict leakage boundary: its signature has no label, split,
    manifest row, or expected outcome parameter.
    """

    try:
        validate_telemetry_batch_v2(batch)
    except (ContractValidationError, FileNotFoundError):
        return _review_core("contract_mismatch")
    samples = batch.get("samples")
    if not isinstance(samples, list) or len(samples) < 2:
        return _review_core("insufficient_samples")
    if not all(isinstance(sample, Mapping) for sample in samples):
        return _review_core("contract_mismatch")
    reasons = _validate_sample_values(samples)
    sent_at = _timestamp(batch.get("sentAt"))
    if sent_at is None:
        reasons.append("missing_timestamps")
    else:
        last_sample_at = _timestamp(samples[-1].get("capturedAt"))
        if last_sample_at is None or sent_at <= last_sample_at:
            reasons.append("non_monotonic_time")
    if reasons:
        return _review_core(_review_reason(reasons))
    try:
        assert sent_at is not None
        numeric = _calculate_numeric_features(samples, sent_at)
    except (AssertionError, ValueError, OverflowError):
        return _review_core("extractor_error")
    if any(not _finite(value) for value in numeric.values()):
        return _review_core("non_finite_value")
    return {"extractionStatus": FEATURE_STATUS_READY, "reasonCode": None, "features": numeric}


def _make_record(
    *,
    batch: Mapping[str, Any],
    core: Mapping[str, Any],
    trace_id: str,
    dataset_hash: str,
    split: str,
    source_kind: str,
    benchmark_eligible: bool,
) -> dict[str, Any]:
    status = str(core["extractionStatus"])
    reason = core.get("reasonCode")
    features = core.get("features")
    record = {
        "schemaVersion": FEATURE_SCHEMA_VERSION,
        "featureId": _uuid_for(dataset_hash, trace_id, FEATURE_EXTRACTOR_VERSION),
        "lineage": _feature_lineage(
            batch=batch,
            trace_id=trace_id,
            dataset_hash=dataset_hash,
            split=split,
            source_kind=source_kind,
            benchmark_eligible=benchmark_eligible,
        ),
        "extractionStatus": status,
        "reasonCode": reason,
        "features": features,
    }
    feature_hash = _record_hash(record)
    if feature_hash is None:
        raise DatasetValidationError("feature record lineage unavailable")
    record["lineage"]["feature"]["featureSha256"] = feature_hash
    return record


def _validate_record_identity(
    *,
    batch: Mapping[str, Any],
    trace_id: str,
    dataset_hash: str,
    split: str,
    source_kind: str,
    benchmark_eligible: bool,
) -> None:
    if not isinstance(batch, Mapping):
        raise DatasetValidationError("feature batch:object")
    batch_id = batch.get("clientBatchId")
    try:
        trace_uuid = str(uuid.UUID(trace_id)) if isinstance(trace_id, str) else None
        batch_uuid = str(uuid.UUID(batch_id)) if isinstance(batch_id, str) else None
    except (ValueError, AttributeError):
        trace_uuid = None
        batch_uuid = None
    valid_hash = (
        isinstance(dataset_hash, str)
        and len(dataset_hash) == 64
        and dataset_hash == dataset_hash.lower()
        and all(character in "0123456789abcdef" for character in dataset_hash)
    )
    if trace_uuid != trace_id:
        raise DatasetValidationError("feature trace identity invalid")
    if not isinstance(batch_id, str) or batch_uuid != batch_id:
        raise DatasetValidationError("feature batch identity invalid")
    if not valid_hash:
        raise DatasetValidationError("feature dataset hash invalid")
    if split not in {"train", "validation", "test"}:
        raise DatasetValidationError("feature split invalid")
    if source_kind not in {"synthetic", "developer_device", "field_pilot", "legacy_import"}:
        raise DatasetValidationError("feature source invalid")
    if not isinstance(benchmark_eligible, bool):
        raise DatasetValidationError("feature benchmark eligibility invalid")


def extract_feature_record(
    batch: Mapping[str, Any],
    *,
    trace_id: str,
    dataset_hash: str,
    split: str,
    source_kind: str = "synthetic",
    benchmark_eligible: bool = True,
) -> dict[str, Any]:
    """Build one contract-shaped feature record from batch-only calculation.

    Lineage metadata is attached after ``calculate_feature_core`` completes;
    labels never enter that function. This full record matches the repository
    ``quality-features.v1`` schema.
    """

    _validate_record_identity(
        batch=batch,
        trace_id=trace_id,
        dataset_hash=dataset_hash,
        split=split,
        source_kind=source_kind,
        benchmark_eligible=benchmark_eligible,
    )
    if source_kind == "developer_device":
        core = _review_core("developer_device_not_benchmarkable")
    elif source_kind != "synthetic" or not benchmark_eligible:
        core = _review_core("unsupported_source")
    else:
        core = calculate_feature_core(batch)
    return _make_record(
        batch=batch,
        core=core,
        trace_id=trace_id,
        dataset_hash=dataset_hash,
        split=split,
        source_kind=source_kind,
        benchmark_eligible=benchmark_eligible,
    )


def _record_feature_hash(record: Mapping[str, Any]) -> str | None:
    lineage = record.get("lineage")
    if not isinstance(lineage, Mapping):
        return None
    feature_lineage = lineage.get("feature")
    if not isinstance(feature_lineage, Mapping):
        return None
    value = feature_lineage.get("featureSha256")
    return value if isinstance(value, str) else None


def verify_feature_hash(record: Mapping[str, Any]) -> bool:
    """Verify feature values and all attached lineage without exposing values."""

    expected = _record_feature_hash(record)
    if expected is None:
        return False
    try:
        return _record_hash(record) == expected
    except (TypeError, ValueError, OverflowError):
        return False


def extract_dataset_features(
    dataset: Mapping[str, Any], manifest: Mapping[str, Any]
) -> dict[str, dict[str, Any]]:
    """Extract feature records and attach frozen lineage outside the core."""

    validate_manifest_against_dataset(manifest, dataset)
    frozen_hash = manifest["datasetSha256"]
    records: dict[str, dict[str, Any]] = {}
    manifest_by_trace = {entry["traceId"]: entry for entry in manifest["traces"]}
    for trace in dataset["traces"]:
        trace_id = trace["traceId"]
        if trace_id in records:
            raise DatasetValidationError("feature trace identity duplicated")
        entry = manifest_by_trace[trace_id]
        records[trace_id] = extract_feature_record(
            trace["batch"],
            trace_id=trace_id,
            dataset_hash=frozen_hash,
            split=entry["split"],
            source_kind=entry["sourceKind"],
            benchmark_eligible=entry["benchmarkEligible"],
        )
        validate_feature_record_schema(records[trace_id])
    return records


def validate_feature_record_schema(
    record: Mapping[str, Any], schema_path: Path | str | None = None
) -> None:
    """Validate against the repository-owned quality-features schema."""

    try:
        schema = (
            Path(schema_path).expanduser().resolve()
            if schema_path is not None
            else find_contract_schema(filename=FEATURE_SCHEMA_FILENAME)
        )
    except FileNotFoundError as error:
        raise FeatureContractUnavailable("quality-features.v1 schema unavailable") from error
    try:
        import jsonschema  # type: ignore[import-not-found]
    except ImportError as error:
        raise FeatureContractUnavailable("quality-features.v1 validator unavailable") from error
    try:
        schema_document = json.loads(schema.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise FeatureContractUnavailable("quality-features.v1 schema unreadable") from error
    errors = list(
        jsonschema.Draft202012Validator(
            schema_document, format_checker=jsonschema.FormatChecker()
        ).iter_errors(record)
    )
    if errors:
        paths = ", ".join(
            ".".join(str(part) for part in error.absolute_path) or "$" for error in errors[:12]
        )
        raise ValueError(f"quality-features.v1 invalid: {paths}")
