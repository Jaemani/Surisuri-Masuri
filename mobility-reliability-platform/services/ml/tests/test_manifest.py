from __future__ import annotations

from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest

from mobility_ml.generate_r07_dataset import generate_dataset
from mobility_ml.manifest import (
    KNOWN_LABELS,
    ContractValidationError,
    DatasetValidationError,
    check_group_time_holdout,
    dataset_sha256,
    find_contract_schema,
    validate_benchmark_dataset,
    validate_dataset_manifest,
    validate_group_time_holdout,
    validate_manifest_against_dataset,
    validate_telemetry_batch_v2,
)


def test_contract_is_found_from_package_context() -> None:
    schema = find_contract_schema(Path(__file__))
    assert schema.name == "telemetry-batch.v2.schema.json"
    assert schema.is_file()


def test_generated_batches_satisfy_checked_in_contract() -> None:
    dataset = generate_dataset()
    for trace in dataset["traces"]:
        validate_telemetry_batch_v2(trace["batch"])


def test_group_and_time_holdout_has_all_known_classes() -> None:
    dataset = generate_dataset()
    assert check_group_time_holdout(dataset["traces"]) == []
    validate_group_time_holdout(dataset["traces"])
    for split in ("validation", "test"):
        assert {trace["label"] for trace in dataset["traces"] if trace["split"] == split} == set(
            KNOWN_LABELS
        )


def test_quality_labels_and_manifest_satisfy_contracts() -> None:
    from mobility_ml.generate_r07_dataset import build_manifest

    dataset = generate_dataset()
    for label in dataset["labels"]:
        from mobility_ml.manifest import validate_quality_label

        validate_quality_label(label)
    validate_dataset_manifest(build_manifest(dataset))


def test_group_leakage_is_rejected_without_leaking_values() -> None:
    dataset = generate_dataset()
    dataset["traces"][0]["split"] = "test"
    with pytest.raises(DatasetValidationError, match="split_leakage"):
        validate_benchmark_dataset(dataset)


def test_developer_device_is_rejected_from_benchmark_loader() -> None:
    dataset = generate_dataset()
    dataset["traces"][0]["sourceKind"] = "developer_device"
    with pytest.raises(DatasetValidationError, match="benchmark_forbidden"):
        validate_benchmark_dataset(dataset)


def test_contract_error_does_not_contain_coordinate_values() -> None:
    dataset = generate_dataset()
    batch = dataset["traces"][0]["batch"]
    batch["samples"][0]["latitude"] = 91
    with pytest.raises(ContractValidationError) as raised:
        validate_telemetry_batch_v2(batch)
    assert "91" not in str(raised.value)
    assert "latitude:maximum" in str(raised.value)


def test_hash_is_stable() -> None:
    first = generate_dataset()
    second = generate_dataset()
    assert first == second
    assert dataset_sha256(first) == dataset_sha256(second)


def test_manifest_hash_and_trace_metadata_are_verified() -> None:
    from mobility_ml.generate_r07_dataset import build_manifest

    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    validate_manifest_against_dataset(manifest, dataset)
    manifest["traces"][0]["telemetrySha256"] = "0" * 64
    with pytest.raises(DatasetValidationError, match="trace metadata:mismatch"):
        validate_manifest_against_dataset(manifest, dataset)


def test_label_batch_linkage_is_verified() -> None:
    dataset = generate_dataset()
    dataset["labels"][0]["telemetryBatchId"] = dataset["traces"][1]["batch"]["clientBatchId"]
    with pytest.raises(DatasetValidationError, match="label_batch_linkage"):
        validate_benchmark_dataset(dataset)


def test_sample_time_controls_holdout_and_must_match_trace_time() -> None:
    dataset = generate_dataset()
    dataset["traces"][0]["batch"]["samples"][0]["capturedAt"] = "2026-09-01T00:00:00Z"
    with pytest.raises(DatasetValidationError, match="capturedAt:(order|sample_linkage)"):
        validate_benchmark_dataset(dataset)


def test_sample_time_cannot_cross_the_temporal_holdout() -> None:
    dataset = generate_dataset()
    trace = dataset["traces"][0]
    start = datetime(2026, 9, 1, tzinfo=UTC)
    for index, sample in enumerate(trace["batch"]["samples"]):
        sample["capturedAt"] = (
            (start + timedelta(seconds=index * 5)).isoformat().replace("+00:00", "Z")
        )
    trace["capturedAt"] = trace["batch"]["samples"][0]["capturedAt"]
    trace["batch"]["sentAt"] = (start + timedelta(seconds=60)).isoformat().replace("+00:00", "Z")
    with pytest.raises(DatasetValidationError, match="train_validation_time_leakage"):
        validate_benchmark_dataset(dataset)


def test_naive_trace_timestamp_fails_closed() -> None:
    dataset = generate_dataset()
    dataset["traces"][0]["capturedAt"] = "2026-08-01T00:00:00"
    with pytest.raises(DatasetValidationError, match="capturedAt:date-time"):
        validate_benchmark_dataset(dataset)


def test_manifest_provenance_is_verified() -> None:
    from mobility_ml.generate_r07_dataset import build_manifest

    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    manifest["generatorVersion"] = "tampered.v9"
    with pytest.raises(DatasetValidationError, match="provenance:mismatch"):
        validate_manifest_against_dataset(manifest, dataset)


def test_dataset_provenance_version_is_verified() -> None:
    dataset = generate_dataset()
    dataset["generatorVersion"] = "tampered.v9"
    with pytest.raises(DatasetValidationError, match="provenance version:mismatch"):
        validate_benchmark_dataset(dataset)


def test_manifest_trace_provenance_is_verified() -> None:
    from mobility_ml.generate_r07_dataset import build_manifest

    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    manifest["traces"][0]["sourceKind"] = "developer_device"
    manifest["traces"][0]["benchmarkEligible"] = False
    with pytest.raises(DatasetValidationError, match="trace metadata:mismatch"):
        validate_manifest_against_dataset(manifest, dataset)


def test_duplicate_trace_and_batch_identity_are_rejected() -> None:
    dataset = generate_dataset()
    dataset["traces"][1]["traceId"] = dataset["traces"][0]["traceId"]
    dataset["labels"][1]["traceId"] = dataset["traces"][0]["traceId"]
    with pytest.raises(DatasetValidationError, match="traceId:missing_or_duplicated"):
        validate_benchmark_dataset(dataset)

    dataset = generate_dataset()
    dataset["traces"][1]["batch"]["clientBatchId"] = dataset["traces"][0]["batch"]["clientBatchId"]
    dataset["labels"][1]["telemetryBatchId"] = dataset["traces"][0]["batch"]["clientBatchId"]
    with pytest.raises(DatasetValidationError, match="telemetryBatchId:duplicated"):
        validate_benchmark_dataset(dataset)


def test_non_object_trace_fails_closed() -> None:
    dataset = generate_dataset()
    dataset["traces"][0] = "malformed"
    with pytest.raises(DatasetValidationError, match=r"trace\[0\]:object"):
        validate_benchmark_dataset(dataset)


@pytest.mark.parametrize("malformed", [None, [], "bad"])
def test_non_object_dataset_fails_closed(malformed: object) -> None:
    with pytest.raises(DatasetValidationError, match="dataset:object"):
        validate_benchmark_dataset(malformed)  # type: ignore[arg-type]


def test_manifest_created_at_is_part_of_deterministic_provenance() -> None:
    from mobility_ml.generate_r07_dataset import build_manifest

    dataset = generate_dataset()
    manifest = build_manifest(dataset)
    manifest["createdAt"] = "2026-08-12T09:00:00Z"
    with pytest.raises(DatasetValidationError, match="provenance:mismatch"):
        validate_manifest_against_dataset(manifest, dataset)
