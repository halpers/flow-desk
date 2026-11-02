import { defineConfig } from "@playwright/test";
import fs from "node:fs";
const settings = Object.fromEntries(
  fs
    .readFileSync(new URL("../.env", import.meta.url), "utf8")
    .split(/\r?\n/)
    .filter((line) => line && !line.startsWith("#"))
    .map((line) => {
      const at = line.indexOf("=");
      return [line.slice(0, at), line.slice(at + 1)];
    }),
);
process.env.FLOWDESK_TEST_PASSWORD = settings.DEMO_PASSWORD;
export default defineConfig({
  testDir: "./tests/e2e",
  workers: 1,
  timeout: 90000,
  use: {
    baseURL:
      process.env.FLOWDESK_E2E_URL ||
      `http://localhost:${settings.WEB_HOST_PORT}`,
    channel: "chrome",
    headless: true,
    trace: "off",
    screenshot: "only-on-failure",
  },
  reporter: "list",
});
