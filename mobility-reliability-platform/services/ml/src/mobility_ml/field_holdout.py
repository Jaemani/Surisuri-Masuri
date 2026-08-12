"""Admission checks for coordinate-free, evaluation-only field holdout manifests."""

from __future__ import annotations

from collections.abc import Mapping
from datetime import UTC, datetime
from typing import Any

from .manifest import DatasetValidationError, _validate_repository_schema

FIELD_HOLDOUT_VERSION = "quality-field-holdout.v1"


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


def validate_field_holdout(manifest: Mapping[str, Any]) -> None:
    """Fail closed unless field metadata is safe for frozen evaluation only.

    Error messages contain field paths and reason codes, never manifest values.
    This function does not load raw telemetry or authorize model deployment.
    """

    _validate_repository_schema(
        manifest,
        filename="quality-field-holdout.v1.schema.json",
        error_type=DatasetValidationError,
        label=FIELD_HOLDOUT_VERSION,
    )
    started = _timestamp(manifest.get("collectionStartedAt"))
    ended = _timestamp(manifest.get("collectionEndedAt"))
    created = _timestamp(manifest.get("createdAt"))
    retention = manifest.get("retention")
    frozen = (
        _timestamp(retention.get("holdoutFrozenAt")) if isinstance(retention, Mapping) else None
    )
    not_before = (
        _timestamp(retention.get("evaluationNotBeforeAt"))
        if isinstance(retention, Mapping)
        else None
    )
    expires = (
        _timestamp(retention.get("evaluationExpiresAt")) if isinstance(retention, Mapping) else None
    )
    boundary = manifest.get("trainingBoundary")
    training_ended = (
        _timestamp(boundary.get("trainingEndedAt")) if isinstance(boundary, Mapping) else None
    )
    if None in (started, ended, created, frozen, not_before, expires, training_ended):
        raise DatasetValidationError("quality-field-holdout.v1 chronology:date-time")
    assert started is not None and ended is not None and created is not None
    assert frozen is not None and not_before is not None and expires is not None
    assert training_ended is not None
    if not training_ended < started <= ended <= frozen <= created <= not_before < expires:
        raise DatasetValidationError("quality-field-holdout.v1 chronology:unsafe")

    traces = manifest.get("traces")
    if not isinstance(traces, list) or manifest.get("traceCount") != len(traces):
        raise DatasetValidationError("quality-field-holdout.v1 traceCount:mismatch")
    seen_trace_ids: set[str] = set()
    seen_batch_ids: set[str] = set()
    groups: set[str] = set()
    for index, trace in enumerate(traces):
        if not isinstance(trace, Mapping):
            raise DatasetValidationError(f"quality-field-holdout.v1 trace[{index}]:object")
        trace_id = trace.get("traceId")
        batch_id = trace.get("telemetryBatchId")
        group_id = trace.get("pseudonymousGroupId")
        if trace_id in seen_trace_ids or batch_id in seen_batch_ids:
            raise DatasetValidationError("quality-field-holdout.v1 identity:duplicate")
        seen_trace_ids.add(str(trace_id))
        seen_batch_ids.add(str(batch_id))
        groups.add(str(group_id))
        captured = _timestamp(trace.get("capturedAt"))
        if captured is None or not started <= captured <= ended:
            raise DatasetValidationError(
                f"quality-field-holdout.v1 trace[{index}].capturedAt:outside_collection"
            )
        label_finalized = _timestamp(trace.get("labelFinalizedAt"))
        if label_finalized is None or label_finalized > frozen:
            raise DatasetValidationError(
                f"quality-field-holdout.v1 trace[{index}].labelFinalizedAt:after_freeze"
            )
        eligible = trace.get("evaluationEligible")
        label_state = trace.get("labelState")
        if eligible is not (label_state == "known"):
            raise DatasetValidationError(
                f"quality-field-holdout.v1 trace[{index}].evaluationEligible:label_state"
            )
    if len(groups) == 0:
        raise DatasetValidationError("quality-field-holdout.v1 groups:missing")
