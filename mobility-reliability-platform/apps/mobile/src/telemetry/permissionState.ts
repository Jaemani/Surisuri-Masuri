export type LocationPermissionState =
  | "checking"
  | "undetermined"
  | "granted"
  | "denied_can_ask"
  | "denied_blocked";

export type LocationPermissionStatus =
  | "undetermined"
  | "granted"
  | "denied";

/**
 * The platform-neutral subset of Expo's Location PermissionResponse used by
 * the telemetry domain. Keeping this boundary free of Expo imports makes the
 * state transition rules independently testable.
 */
export interface LocationPermissionSnapshot {
  status: LocationPermissionStatus;
  canAskAgain: boolean;
  granted: boolean;
}

/**
 * Converts an Expo-compatible permission response into an application state.
 *
 * `status` is authoritative. Expo also exposes the derived `granted` boolean,
 * but using it as a second authority could turn an inconsistent native response
 * into an unsafe grant. The boolean is therefore accepted for shape
 * compatibility but never overrides `status`.
 */
export function toPermissionState(
  permission: LocationPermissionSnapshot | null | undefined,
): LocationPermissionState {
  if (permission == null) {
    return "checking";
  }

  switch (permission.status) {
    case "granted":
      return "granted";
    case "undetermined":
      return "undetermined";
    case "denied":
      return permission.canAskAgain ? "denied_can_ask" : "denied_blocked";
  }
}
