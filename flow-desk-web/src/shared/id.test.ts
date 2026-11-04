import { afterEach, expect, it, vi } from "vitest";
import { newId } from "./id";

afterEach(() => vi.unstubAllGlobals());

it("generates distinct UUID v4 request identities when HTTP disables randomUUID", () => {
  const nativeCrypto = globalThis.crypto;
  const getRandomValues = vi.fn((bytes: Uint8Array) =>
    nativeCrypto.getRandomValues(bytes),
  );
  vi.stubGlobal("crypto", { getRandomValues });
  const identities = Array.from({ length: 100 }, () => newId());
  expect(new Set(identities).size).toBe(100);
  for (const identity of identities) {
    expect(identity).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  }
  expect(getRandomValues).toHaveBeenCalledTimes(100);
});
