"""Coordinate-free feature bridge for admitted field holdout traces."""

from __future__ import annotations

import hashlib
import uuid
from collections.abc import Mapping
from typing import Any

from .features import FEATURE_EXTRACTOR_VERSION, calculate_feature_core
from .field_holdout import validate_field_holdout
from .manifest import (
    DatasetValidationError,
    _validate_repository_schema,
    canonical_json,
    dataset_sha256,
    validate_telemetry_batch_v2,
)

FIELD_FEATURE_VERSION = "quality-field-features.v1"
FIELD_FEATURE_NAMESPACE = uuid.UUID("7a7c1c15-8731-53ad-8d2b-c012d6b6822c")


def _without_feature_hash(record: Mapping[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in record.items() if key != "featureSha256"}


def verify_field_feature_hash(record: Mapping[str, Any]) -> bool:
    expected = record.get("featureSha256")
    if not isinstance(expected, str):
        return False
    try:
        actual = hashlib.sha256(canonical_json(_without_feature_hash(record))).hexdigest()
    except (TypeError, ValueError, OverflowError):
        return False
    return actual == expected


def validate_field_feature_schema(record: Mapping[str, Any]) -> None:
    _validate_repository_schema(
        record,
        filename="quality-field-features.v1.schema.json",
        error_type=DatasetValidationError,
        label=FIELD_FEATURE_VERSION,
    )
    if not verify_field_feature_hash(record):
        raise DatasetValidationError("quality-field-features.v1 featureSha256:mismatch")


def extract_field_feature_record(
    batch: Mapping[str, Any],
    holdout_manifest: Mapping[str, Any],
    *,
    trace_id: str,
) -> dict[str, Any]:
    """Extract numeric features after exact admitted trace/batch linkage checks.

    The numeric core transiently reads coordinates but this record never
    returns them. Labels and pseudonymous group identifiers are not passed to
    the calculation boundary or copied to its output.
    """

    validate_field_holdout(holdout_manifest)
    entries = [entry for entry in holdout_manifest["traces"] if entry.get("traceId") == trace_id]
    if len(entries) != 1:
        raise DatasetValidationError("field feature trace linkage:mismatch")
    entry = entries[0]
    validate_telemetry_batch_v2(batch)
    if batch.get("clientBatchId") != entry.get("telemetryBatchId"):
        raise DatasetValidationError("field feature batch identity:mismatch")
    if dataset_sha256(batch) != entry.get("telemetrySha256"):
        raise DatasetValidationError("field feature telemetrySha256:mismatch")
    core = calculate_feature_core(batch)
    manifest_sha = dataset_sha256(holdout_manifest)
    record: dict[str, Any] = {
        "schemaVersion": FIELD_FEATURE_VERSION,
        "featureId": str(uuid.uuid5(FIELD_FEATURE_NAMESPACE, f"{manifest_sha}:{trace_id}")),
        "holdoutId": holdout_manifest["holdoutId"],
        "holdoutManifestSha256": manifest_sha,
        "traceId": trace_id,
        "telemetryBatchId": entry["telemetryBatchId"],
        "telemetrySha256": entry["telemetrySha256"],
        "extractorVersion": FEATURE_EXTRACTOR_VERSION,
        "extractionStatus": core["extractionStatus"],
        "reasonCode": core["reasonCode"],
        "features": core["features"],
    }
    record["featureSha256"] = hashlib.sha256(canonical_json(record)).hexdigest()
    validate_field_feature_schema(record)
    return record
