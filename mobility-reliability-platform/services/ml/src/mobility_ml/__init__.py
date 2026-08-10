"""Reproducible ML data utilities for the mobility reliability platform.

The R07 dataset is deliberately synthetic.  Its artifacts are useful for
contract, split, and pipeline checks, but must never be reported as field
performance or user behaviour.
"""

from .features import (
    FEATURE_EXTRACTOR_VERSION,
    FEATURE_SCHEMA_VERSION,
    FEATURE_STATUS_READY,
    FEATURE_STATUS_REVIEW,
    FeatureContractUnavailable,
    FeatureLeakageError,
    extract_dataset_features,
    extract_feature_record,
    validate_feature_record_schema,
    verify_feature_hash,
)
from .manifest import (
    DATASET_VERSION,
    FEATURE_VERSION,
    GENERATOR_VERSION,
    KNOWN_LABELS,
    SPLIT_STRATEGY,
    ContractValidationError,
    DatasetValidationError,
    dataset_sha256,
    find_contract_schema,
    validate_dataset_manifest,
    validate_group_time_holdout,
    validate_manifest_against_dataset,
    validate_quality_label,
    validate_telemetry_batch_v2,
)
from .rules_baseline import (
    METRICS_VERSION,
    RULE_BASELINE_VERSION,
    evaluate_frozen_dataset,
    predict_rule,
    validate_baseline_result_schema,
)

__all__ = [
    "KNOWN_LABELS",
    "DATASET_VERSION",
    "FEATURE_VERSION",
    "GENERATOR_VERSION",
    "SPLIT_STRATEGY",
    "ContractValidationError",
    "DatasetValidationError",
    "dataset_sha256",
    "find_contract_schema",
    "validate_dataset_manifest",
    "validate_group_time_holdout",
    "validate_manifest_against_dataset",
    "validate_quality_label",
    "validate_telemetry_batch_v2",
    "FEATURE_EXTRACTOR_VERSION",
    "FEATURE_SCHEMA_VERSION",
    "FEATURE_STATUS_READY",
    "FEATURE_STATUS_REVIEW",
    "FeatureContractUnavailable",
    "FeatureLeakageError",
    "extract_dataset_features",
    "extract_feature_record",
    "validate_feature_record_schema",
    "verify_feature_hash",
    "METRICS_VERSION",
    "RULE_BASELINE_VERSION",
    "evaluate_frozen_dataset",
    "predict_rule",
    "validate_baseline_result_schema",
]
