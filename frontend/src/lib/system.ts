import { Browser, System } from "@wailsio/runtime";

/** Opens the OS notification settings (macOS and Windows). */
export function openNotificationSettings(): void {
  if (System.IsMac()) {
    Browser.OpenURL("x-apple.systempreferences:com.apple.Notifications-Settings.extension");
  } else if (System.IsWindows()) {
    Browser.OpenURL("ms-settings:notifications");
  }
}
