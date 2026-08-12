"""Deterministic synthetic episodes for the R10 time-to-inspection baseline.

The records intentionally contain no raw GPS, Firebase identity, tenant identity,
free text, or legacy repair payload.  They exercise dataset lineage and split
validation only; they are not field observations.
"""

from __future__ import annotations

import math
import random
import uuid
from collections.abc import Mapping
from datetime import UTC, datetime, timedelta
from typing import Any

from .manifest import DatasetValidationError, dataset_sha256

DATASET_VERSION = "reliability-dataset.r10.synthetic.v1"
GENERATOR_VERSION = "r10-reliability-generator.v1"
OUTCOME_VERSION = "time-to-inspection-outcome.v1"
SPLIT_STRATEGY = "device-group-time-holdout.v1"
DEFAULT_SEED = 20260813
HORIZON_DAYS = 30
COMPONENTS = ("battery", "brake", "controller")
SPLIT_STARTS = {
    "train": datetime(2024, 1, 1, tzinfo=UTC),
    "validation": datetime(2025, 1, 1, tzinfo=UTC),
    "test": datetime(2026, 1, 1, tzinfo=UTC),
}

_FORBIDDEN_KEYS = {
    "latitude",
    "longitude",
    "coordinates",
    "firebaseUid",
    "tenantId",
    "userId",
    "phoneNumber",
    "repairMemo",
}


def _iso(value: datetime) -> str:
    return value.astimezone(UTC).isoformat().replace("+00:00", "Z")


def _timestamp(value: Any, label: str) -> datetime:
    if not isinstance(value, str):
        raise DatasetValidationError(f"reliability episode {label}:date-time")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise DatasetValidationError(f"reliability episode {label}:date-time") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise DatasetValidationError(f"reliability episode {label}:date-time")
    return parsed.astimezone(UTC)


def _contains_forbidden_key(value: Any) -> bool:
    if isinstance(value, Mapping):
        return bool(_FORBIDDEN_KEYS.intersection(value)) or any(
            _contains_forbidden_key(child) for child in value.values()
        )
    if isinstance(value, list):
        return any(_contains_forbidden_key(child) for child in value)
    return False


def generate_reliability_dataset(seed: int = DEFAULT_SEED) -> dict[str, Any]:
    """Generate deterministic, coordinate-free component observation episodes."""

    rng = random.Random(seed)
    episodes: list[dict[str, Any]] = []
    replacement_events: list[dict[str, Any]] = []
    component_sizes = {"battery": 8, "brake": 6, "controller": 3}
    namespace = uuid.UUID("30ef5dd1-0878-44fb-b32e-c84b2b579c27")
    for split_index, split in enumerate(("train", "validation", "test")):
        cursor = 0
        for component_index, component in enumerate(COMPONENTS):
            for index in range(component_sizes[component]):
                identity = f"{seed}:{split}:{component}:{index}"
                group_id = str(uuid.uuid5(namespace, f"group:{seed}:{split}:{index}"))
                decision_at = SPLIT_STARTS[split] + timedelta(days=cursor * 3 + 45)
                cursor += 1
                age_days = 105 + index * 22 + component_index * 9 + split_index * 4
                daily_distance = 3500 + index * 650 + component_index * 300
                distance = 520_000 + index * 105_000 + component_index * 35_000
                # The outcome is deterministic but not identical to either rule,
                # leaving useful false-positive and false-negative examples.
                event_observed = (index + component_index + split_index) % 4 != 0
                if component == "controller" and index == 2:
                    event_observed = False
                observed_through = decision_at + timedelta(days=HORIZON_DAYS)
                reset_by_replacement = index == 0 and split != "train"
                episode: dict[str, Any] = {
                    "episodeId": str(uuid.uuid5(namespace, f"episode:{identity}")),
                    "deviceGroupId": group_id,
                    "component": component,
                    "split": split,
                    "riskStartAt": _iso(decision_at - timedelta(days=age_days)),
                    "riskStartReason": (
                        "component_replaced" if reset_by_replacement else "observation_started"
                    ),
                    "decisionAt": _iso(decision_at),
                    "distanceSummaryAsOfAt": _iso(decision_at),
                    "observedThroughAt": _iso(observed_through),
                    "cumulativeDistanceM": distance,
                    "meanDailyDistanceM": daily_distance,
                    "eventObserved": event_observed,
                }
                if reset_by_replacement:
                    reset_event_id = str(uuid.uuid5(namespace, f"replacement:{identity}"))
                    episode["riskResetEventId"] = reset_event_id
                    replacement_events.append(
                        {
                            "eventId": reset_event_id,
                            "deviceGroupId": group_id,
                            "component": component,
                            "occurredAt": episode["riskStartAt"],
                            "sourceQuality": "verified_synthetic",
                        }
                    )
                if event_observed:
                    event_offset = 6 + rng.randrange(20)
                    episode["outcomeAt"] = _iso(decision_at + timedelta(days=event_offset))
                    episode["outcomeKind"] = "inspection_required"
                episodes.append(episode)
    dataset = {
        "schemaVersion": DATASET_VERSION,
        "generatorVersion": GENERATOR_VERSION,
        "outcomeDefinitionVersion": OUTCOME_VERSION,
        "splitStrategy": SPLIT_STRATEGY,
        "sourceKind": "synthetic",
        "seed": seed,
        "horizonDays": HORIZON_DAYS,
        "episodes": episodes,
        "replacementEvents": replacement_events,
    }
    validate_reliability_dataset(dataset)
    return dataset


def validate_device_group_time_holdout(episodes: list[Mapping[str, Any]]) -> None:
    """Fail closed on group overlap, future-label leakage, or split chronology."""

    split_groups: dict[str, set[str]] = {split: set() for split in SPLIT_STARTS}
    decisions: dict[str, list[datetime]] = {split: [] for split in SPLIT_STARTS}
    label_available: dict[str, list[datetime]] = {split: [] for split in SPLIT_STARTS}
    group_split: dict[str, str] = {}
    for episode in episodes:
        split = episode.get("split")
        group = episode.get("deviceGroupId")
        if split not in SPLIT_STARTS or not isinstance(group, str):
            raise DatasetValidationError("reliability split or device group:invalid")
        previous = group_split.setdefault(group, split)
        if previous != split:
            raise DatasetValidationError("reliability device group:split_leakage")
        split_groups[split].add(group)
        decision = _timestamp(episode.get("decisionAt"), "decisionAt")
        available = _timestamp(episode.get("observedThroughAt"), "observedThroughAt")
        decisions[split].append(decision)
        label_available[split].append(available)
    if any(not decisions[split] for split in SPLIT_STARTS):
        raise DatasetValidationError("reliability split:missing")
    if max(label_available["train"]) >= min(decisions["validation"]):
        raise DatasetValidationError("reliability train label:future_leakage")
    if max(label_available["validation"]) >= min(decisions["test"]):
        raise DatasetValidationError("reliability validation label:future_leakage")


def validate_reliability_dataset(dataset: Mapping[str, Any]) -> None:
    """Validate internal R10 dataset semantics without logging record values."""

    expected_root = {
        "schemaVersion": DATASET_VERSION,
        "generatorVersion": GENERATOR_VERSION,
        "outcomeDefinitionVersion": OUTCOME_VERSION,
        "splitStrategy": SPLIT_STRATEGY,
        "sourceKind": "synthetic",
        "horizonDays": HORIZON_DAYS,
    }
    if any(dataset.get(key) != value for key, value in expected_root.items()):
        raise DatasetValidationError("reliability dataset provenance:mismatch")
    if set(dataset) != {*expected_root, "seed", "episodes", "replacementEvents"}:
        raise DatasetValidationError("reliability dataset keys:invalid")
    if _contains_forbidden_key(dataset):
        raise DatasetValidationError("reliability dataset forbidden identity or location key")
    seed = dataset.get("seed")
    episodes = dataset.get("episodes")
    replacement_events = dataset.get("replacementEvents")
    if not isinstance(seed, int) or isinstance(seed, bool) or seed < 0:
        raise DatasetValidationError("reliability dataset seed:invalid")
    if not isinstance(episodes, list) or not episodes:
        raise DatasetValidationError("reliability episodes:missing")
    if not isinstance(replacement_events, list):
        raise DatasetValidationError("reliability replacement events:invalid")
    replacement_by_id: dict[str, Mapping[str, Any]] = {}
    for event in replacement_events:
        if not isinstance(event, Mapping) or set(event) != {
            "eventId",
            "deviceGroupId",
            "component",
            "occurredAt",
            "sourceQuality",
        }:
            raise DatasetValidationError("reliability replacement event:invalid")
        event_id = event.get("eventId")
        try:
            if str(uuid.UUID(str(event_id))) != event_id:
                raise ValueError
        except ValueError as error:
            raise DatasetValidationError(
                "reliability replacement event identity:invalid"
            ) from error
        if event_id in replacement_by_id:
            raise DatasetValidationError("reliability replacement event identity:duplicate")
        if event.get("component") not in COMPONENTS:
            raise DatasetValidationError("reliability replacement event component:invalid")
        if event.get("sourceQuality") != "verified_synthetic":
            raise DatasetValidationError("reliability replacement event provenance:invalid")
        _timestamp(event.get("occurredAt"), "replacement occurredAt")
        replacement_by_id[event_id] = event
    seen_episode_ids: set[str] = set()
    for episode in episodes:
        if not isinstance(episode, Mapping):
            raise DatasetValidationError("reliability episode:object")
        allowed = {
            "episodeId",
            "deviceGroupId",
            "component",
            "split",
            "riskStartAt",
            "riskStartReason",
            "riskResetEventId",
            "decisionAt",
            "distanceSummaryAsOfAt",
            "observedThroughAt",
            "cumulativeDistanceM",
            "meanDailyDistanceM",
            "eventObserved",
            "outcomeAt",
            "outcomeKind",
        }
        if set(episode) - allowed:
            raise DatasetValidationError("reliability episode keys:invalid")
        for key in ("episodeId", "deviceGroupId"):
            try:
                if str(uuid.UUID(str(episode.get(key)))) != episode.get(key):
                    raise ValueError
            except ValueError as error:
                raise DatasetValidationError(f"reliability episode {key}:uuid") from error
        if episode["episodeId"] in seen_episode_ids:
            raise DatasetValidationError("reliability episode identity:duplicate")
        seen_episode_ids.add(episode["episodeId"])
        if episode.get("component") not in COMPONENTS:
            raise DatasetValidationError("reliability episode component:unknown")
        risk_start = _timestamp(episode.get("riskStartAt"), "riskStartAt")
        decision = _timestamp(episode.get("decisionAt"), "decisionAt")
        distance_as_of = _timestamp(episode.get("distanceSummaryAsOfAt"), "distanceSummaryAsOfAt")
        observed_through = _timestamp(episode.get("observedThroughAt"), "observedThroughAt")
        if not risk_start < decision < observed_through:
            raise DatasetValidationError("reliability episode chronology:invalid")
        if distance_as_of != decision:
            raise DatasetValidationError("reliability distance summary:future_leakage")
        if (observed_through - decision).days != HORIZON_DAYS:
            raise DatasetValidationError("reliability episode horizon:mismatch")
        reset_reason = episode.get("riskStartReason")
        reset_event = episode.get("riskResetEventId")
        if reset_reason == "component_replaced":
            event = replacement_by_id.get(reset_event)
            if event is None:
                raise DatasetValidationError("reliability risk reset event:missing")
            if (
                event["deviceGroupId"] != episode["deviceGroupId"]
                or event["component"] != episode["component"]
                or event["occurredAt"] != episode["riskStartAt"]
            ):
                raise DatasetValidationError("reliability risk reset event:linkage")
        elif reset_reason == "observation_started":
            if reset_event is not None:
                raise DatasetValidationError("reliability risk reset event:unexpected")
        else:
            raise DatasetValidationError("reliability risk start reason:invalid")
        for key in ("cumulativeDistanceM", "meanDailyDistanceM"):
            value = episode.get(key)
            if (
                not isinstance(value, int | float)
                or isinstance(value, bool)
                or not math.isfinite(value)
                or value < 0
            ):
                raise DatasetValidationError(f"reliability episode {key}:invalid")
        event_observed = episode.get("eventObserved")
        if not isinstance(event_observed, bool):
            raise DatasetValidationError("reliability episode eventObserved:boolean")
        if event_observed:
            outcome = _timestamp(episode.get("outcomeAt"), "outcomeAt")
            if not decision <= outcome <= observed_through:
                raise DatasetValidationError("reliability episode outcome chronology:invalid")
            if episode.get("outcomeKind") != "inspection_required":
                raise DatasetValidationError("reliability episode outcome kind:invalid")
        elif "outcomeAt" in episode or "outcomeKind" in episode:
            raise DatasetValidationError("reliability censored episode outcome:unexpected")
    validate_device_group_time_holdout(episodes)


def reliability_dataset_hash(dataset: Mapping[str, Any]) -> str:
    validate_reliability_dataset(dataset)
    return dataset_sha256(dataset)
