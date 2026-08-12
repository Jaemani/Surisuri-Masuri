from __future__ import annotations

import json
from pathlib import Path

import pytest

from mobility_ml.field_holdout import validate_field_holdout
from mobility_ml.manifest import DatasetValidationError, find_contract_schema


def fixture() -> dict:
    path = find_contract_schema(filename="quality-field-holdout.v1.schema.json")
    return json.loads(
        (path.parent.parent / "fixtures/quality-field-holdout.v1.valid.json").read_text()
    )


def test_valid_coordinate_free_field_holdout_is_admitted_for_evaluation() -> None:
    validate_field_holdout(fixture())


@pytest.mark.parametrize(
    "field", ["trainingEligible", "deploymentEligible", "rawCoordinatesIncluded"]
)
def test_training_or_coordinates_fail_at_schema_boundary(field: str) -> None:
    manifest = fixture()
    manifest[field] = True
    with pytest.raises(DatasetValidationError, match=field):
        validate_field_holdout(manifest)


def test_field_collection_must_follow_frozen_training() -> None:
    manifest = fixture()
    manifest["trainingBoundary"]["trainingEndedAt"] = "2026-09-02T00:00:00Z"
    with pytest.raises(DatasetValidationError, match="chronology:unsafe"):
        validate_field_holdout(manifest)


def test_trace_count_label_freeze_and_time_are_verified() -> None:
    manifest = fixture()
    manifest["traceCount"] = 2
    with pytest.raises(DatasetValidationError, match="traceCount:mismatch"):
        validate_field_holdout(manifest)

    manifest = fixture()
    manifest["traces"][0]["labelFinalizedAt"] = "2026-09-10T08:00:01Z"
    with pytest.raises(DatasetValidationError, match="labelFinalizedAt:after_freeze"):
        validate_field_holdout(manifest)

    manifest = fixture()
    manifest["traces"][0]["capturedAt"] = "2026-08-31T23:59:59Z"
    with pytest.raises(DatasetValidationError, match="outside_collection"):
        validate_field_holdout(manifest)


def test_review_state_cannot_enter_metric_evaluation() -> None:
    manifest = fixture()
    manifest["traces"][0]["labelState"] = "review_required"
    manifest["traces"][0]["expectedLabel"] = None
    with pytest.raises(DatasetValidationError, match="labelEligible:const"):
        validate_field_holdout(manifest)


def test_unknown_fields_and_raw_coordinate_names_are_rejected_without_values() -> None:
    manifest = fixture()
    manifest["latitude"] = 37.123456
    with pytest.raises(DatasetValidationError) as raised:
        validate_field_holdout(manifest)
    assert "additionalProperties" in str(raised.value)
    assert "37.123456" not in str(raised.value)


def test_schema_is_discoverable_from_an_unrelated_path() -> None:
    assert find_contract_schema(Path(__file__), "quality-field-holdout.v1.schema.json").is_file()
