import { describe, expect, it } from "vitest";

import { toPermissionState } from "./permissionState";

describe("toPermissionState", () => {
  it.each([null, undefined])(
    "returns checking while no permission response is available (%s)",
    (permission) => {
      expect(toPermissionState(permission)).toBe("checking");
    },
  );

  it("keeps an unrequested permission undetermined", () => {
    expect(
      toPermissionState({
        status: "undetermined",
        canAskAgain: true,
        granted: false,
      }),
    ).toBe("undetermined");
  });

  it("maps an explicit grant to granted", () => {
    expect(
      toPermissionState({
        status: "granted",
        canAskAgain: true,
        granted: true,
      }),
    ).toBe("granted");
  });

  it.each([
    { canAskAgain: true, expected: "denied_can_ask" },
    { canAskAgain: false, expected: "denied_blocked" },
  ] as const)(
    "maps denial with canAskAgain=$canAskAgain to $expected",
    ({ canAskAgain, expected }) => {
      expect(
        toPermissionState({
          status: "denied",
          canAskAgain,
          granted: false,
        }),
      ).toBe(expected);
    },
  );

  describe("inconsistent native responses", () => {
    it("trusts granted status when the redundant boolean is false", () => {
      expect(
        toPermissionState({
          status: "granted",
          canAskAgain: true,
          granted: false,
        }),
      ).toBe("granted");
    });

    it("does not grant access when denied status conflicts with granted=true", () => {
      expect(
        toPermissionState({
          status: "denied",
          canAskAgain: false,
          granted: true,
        }),
      ).toBe("denied_blocked");
    });

    it("keeps undetermined status when it conflicts with granted=true", () => {
      expect(
        toPermissionState({
          status: "undetermined",
          canAskAgain: true,
          granted: true,
        }),
      ).toBe("undetermined");
    });
  });
});
