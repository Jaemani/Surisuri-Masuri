import { describe, expect, it } from "vitest";

import {
  evaluateLocationSample,
  LATITUDE_MAX_DEGREES,
  LATITUDE_MIN_DEGREES,
  LONGITUDE_MAX_DEGREES,
  LONGITUDE_MIN_DEGREES,
  MAX_DATE_TIMESTAMP_MILLISECONDS,
  MAX_HEADING_DEGREES_EXCLUSIVE,
  MAX_HORIZONTAL_ACCURACY_METERS,
  MIN_HEADING_DEGREES,
  MIN_SPEED_METERS_PER_SECOND,
  type RawTelemetrySample,
} from "./samplePolicy";

const validSample: RawTelemetrySample = {
  latitude: 37.5665,
  longitude: 126.978,
  timestamp: 1_721_564_800_000,
  accuracy: 12,
  altitude: 38,
  speed: 1.5,
  heading: 90,
  isMockLocation: null,
};

function sampleWith(
  overrides: Partial<RawTelemetrySample>,
): RawTelemetrySample {
  return { ...validSample, ...overrides };
}

describe("evaluateLocationSample", () => {
  describe("coordinates", () => {
    it.each([
      [LATITUDE_MIN_DEGREES, LONGITUDE_MIN_DEGREES],
      [LATITUDE_MAX_DEGREES, LONGITUDE_MAX_DEGREES],
      [0, 0],
    ])("accepts coordinate boundaries (%s, %s)", (latitude, longitude) => {
      expect(
        evaluateLocationSample(sampleWith({ latitude, longitude })),
      ).toMatchObject({ accepted: true });
    });

    it.each([
      [LATITUDE_MIN_DEGREES - 0.000_001, 0],
      [LATITUDE_MAX_DEGREES + 0.000_001, 0],
      [0, LONGITUDE_MIN_DEGREES - 0.000_001],
      [0, LONGITUDE_MAX_DEGREES + 0.000_001],
      [Number.NaN, 0],
      [Number.POSITIVE_INFINITY, 0],
      [0, Number.NEGATIVE_INFINITY],
    ])(
      "rejects an invalid coordinate without echoing it",
      (latitude, longitude) => {
        expect(
          evaluateLocationSample(sampleWith({ latitude, longitude })),
        ).toEqual({
          accepted: false,
          reason: "invalid_coordinate",
        });
      },
    );
  });

  describe("timestamp", () => {
    it("accepts the smallest positive finite timestamp", () => {
      expect(
        evaluateLocationSample(sampleWith({ timestamp: Number.MIN_VALUE })),
      ).toMatchObject({ accepted: true });
    });

    it("accepts the largest timestamp representable by Date", () => {
      expect(
        evaluateLocationSample(
          sampleWith({ timestamp: MAX_DATE_TIMESTAMP_MILLISECONDS }),
        ),
      ).toMatchObject({ accepted: true });
    });

    it.each([
      0,
      -Number.MIN_VALUE,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
      MAX_DATE_TIMESTAMP_MILLISECONDS + 1,
    ])("rejects invalid timestamp %s", (timestamp) => {
      expect(evaluateLocationSample(sampleWith({ timestamp }))).toEqual({
        accepted: false,
        reason: "invalid_timestamp",
      });
    });

    it("rejects timestamps outside the current session window", () => {
      expect(
        evaluateLocationSample(sampleWith({ timestamp: 9_999 }), {
          minimumTimestamp: 10_000,
        }),
      ).toEqual({ accepted: false, reason: "invalid_timestamp" });
      expect(
        evaluateLocationSample(sampleWith({ timestamp: 20_001 }), {
          maximumTimestamp: 20_000,
        }),
      ).toEqual({ accepted: false, reason: "invalid_timestamp" });
    });
  });

  describe("horizontal accuracy", () => {
    it.each([null, 0, MAX_HORIZONTAL_ACCURACY_METERS])(
      "accepts accuracy boundary %s",
      (accuracy) => {
        expect(evaluateLocationSample(sampleWith({ accuracy }))).toMatchObject({
          accepted: true,
        });
      },
    );

    it.each([
      -Number.MIN_VALUE,
      MAX_HORIZONTAL_ACCURACY_METERS + 0.000_001,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
    ])("rejects unusable accuracy %s", (accuracy) => {
      expect(evaluateLocationSample(sampleWith({ accuracy }))).toEqual({
        accepted: false,
        reason: "poor_accuracy",
      });
    });
  });

  describe("optional sensor normalization", () => {
    it.each([true, false, null])("preserves mock-location signal %s", (isMockLocation) => {
      expect(
        evaluateLocationSample(sampleWith({ isMockLocation })),
      ).toMatchObject({ accepted: true, sample: { isMockLocation } });
    });
    it.each([null, -500, 0, 8_848.86])(
      "preserves finite or null altitude %s",
      (altitude) => {
        expect(evaluateLocationSample(sampleWith({ altitude }))).toMatchObject({
          accepted: true,
          sample: { altitude },
        });
      },
    );

    it.each([Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY])(
      "normalizes non-finite altitude %s to null",
      (altitude) => {
        expect(evaluateLocationSample(sampleWith({ altitude }))).toMatchObject({
          accepted: true,
          sample: { altitude: null },
        });
      },
    );

    it.each([null, MIN_SPEED_METERS_PER_SECOND, Number.MAX_VALUE])(
      "preserves non-negative finite or null speed %s",
      (speed) => {
        expect(evaluateLocationSample(sampleWith({ speed }))).toMatchObject({
          accepted: true,
          sample: { speed },
        });
      },
    );

    it.each([
      -Number.MIN_VALUE,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
    ])("normalizes invalid speed %s to null", (speed) => {
      expect(evaluateLocationSample(sampleWith({ speed }))).toMatchObject({
        accepted: true,
        sample: { speed: null },
      });
    });

    it.each([
      null,
      MIN_HEADING_DEGREES,
      MAX_HEADING_DEGREES_EXCLUSIVE - Number.EPSILON * 256,
    ])("preserves valid heading boundary %s", (heading) => {
      expect(evaluateLocationSample(sampleWith({ heading }))).toMatchObject({
        accepted: true,
        sample: { heading },
      });
    });

    it.each([
      -Number.MIN_VALUE,
      MAX_HEADING_DEGREES_EXCLUSIVE,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
    ])("normalizes invalid heading %s to null", (heading) => {
      expect(evaluateLocationSample(sampleWith({ heading }))).toMatchObject({
        accepted: true,
        sample: { heading: null },
      });
    });
  });

  it("returns a fresh normalized sample without mutating the input", () => {
    const input = sampleWith({ speed: -1, heading: -1 });
    const before = { ...input };
    const result = evaluateLocationSample(input);

    expect(input).toEqual(before);
    expect(result).toEqual({
      accepted: true,
      sample: { ...before, speed: null, heading: null },
    });
    if (result.accepted) {
      expect(result.sample).not.toBe(input);
    }
  });
});
