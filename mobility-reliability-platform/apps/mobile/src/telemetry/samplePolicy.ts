export const LATITUDE_MIN_DEGREES = -90;
export const LATITUDE_MAX_DEGREES = 90;
export const LONGITUDE_MIN_DEGREES = -180;
export const LONGITUDE_MAX_DEGREES = 180;
export const MAX_HORIZONTAL_ACCURACY_METERS = 100;
export const MAX_DATE_TIMESTAMP_MILLISECONDS = 8_640_000_000_000_000;
export const MAX_FUTURE_CLOCK_SKEW_MILLISECONDS = 5 * 60 * 1_000;
export const MIN_SPEED_METERS_PER_SECOND = 0;
export const MIN_HEADING_DEGREES = 0;
export const MAX_HEADING_DEGREES_EXCLUSIVE = 360;

export type RawTelemetrySample = {
  latitude: number;
  longitude: number;
  timestamp: number;
  accuracy: number | null;
  altitude: number | null;
  speed: number | null;
  heading: number | null;
  isMockLocation: boolean | null;
};

export type NormalizedLocationSample = RawTelemetrySample;

export type SampleRejectionReason =
  | "invalid_coordinate"
  | "invalid_timestamp"
  | "poor_accuracy";

export type SamplePolicyResult =
  | { accepted: true; sample: NormalizedLocationSample }
  | { accepted: false; reason: SampleRejectionReason };

export type SampleTemporalContext = {
  minimumTimestamp?: number;
  maximumTimestamp?: number;
};

function isInClosedRange(value: number, minimum: number, maximum: number) {
  return Number.isFinite(value) && value >= minimum && value <= maximum;
}

function normalizeFinite(value: number | null): number | null {
  return value !== null && Number.isFinite(value) ? value : null;
}

function normalizeSpeed(speed: number | null): number | null {
  return speed !== null &&
    Number.isFinite(speed) &&
    speed >= MIN_SPEED_METERS_PER_SECOND
    ? speed
    : null;
}

function normalizeHeading(heading: number | null): number | null {
  return heading !== null &&
    Number.isFinite(heading) &&
    heading >= MIN_HEADING_DEGREES &&
    heading < MAX_HEADING_DEGREES_EXCLUSIVE
    ? heading
    : null;
}

/**
 * Validates fields that determine whether a sample can enter the outbox and
 * normalizes optional sensor values. Rejections intentionally carry no input
 * values so callers cannot accidentally expose coordinates through logs.
 */
export function evaluateLocationSample(
  input: RawTelemetrySample,
  temporalContext: SampleTemporalContext = {},
): SamplePolicyResult {
  const hasValidCoordinates =
    isInClosedRange(
      input.latitude,
      LATITUDE_MIN_DEGREES,
      LATITUDE_MAX_DEGREES,
    ) &&
    isInClosedRange(
      input.longitude,
      LONGITUDE_MIN_DEGREES,
      LONGITUDE_MAX_DEGREES,
    );

  if (!hasValidCoordinates) {
    return { accepted: false, reason: "invalid_coordinate" };
  }

  if (
    !Number.isFinite(input.timestamp) ||
    input.timestamp <= 0 ||
    input.timestamp > MAX_DATE_TIMESTAMP_MILLISECONDS ||
    (temporalContext.minimumTimestamp !== undefined &&
      input.timestamp < temporalContext.minimumTimestamp) ||
    (temporalContext.maximumTimestamp !== undefined &&
      input.timestamp > temporalContext.maximumTimestamp)
  ) {
    return { accepted: false, reason: "invalid_timestamp" };
  }

  if (
    input.accuracy !== null &&
    (!Number.isFinite(input.accuracy) ||
      input.accuracy < 0 ||
      input.accuracy > MAX_HORIZONTAL_ACCURACY_METERS)
  ) {
    return { accepted: false, reason: "poor_accuracy" };
  }

  return {
    accepted: true,
    sample: {
      latitude: input.latitude,
      longitude: input.longitude,
      timestamp: input.timestamp,
      accuracy: input.accuracy,
      altitude: normalizeFinite(input.altitude),
      speed: normalizeSpeed(input.speed),
      heading: normalizeHeading(input.heading),
      isMockLocation:
        typeof input.isMockLocation === "boolean" ? input.isMockLocation : null,
    },
  };
}
