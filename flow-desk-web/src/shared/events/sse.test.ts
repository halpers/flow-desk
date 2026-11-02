import { describe, expect, it } from "vitest";
import {
  acceptSequence,
  decodeRunEvent,
  SSEParser,
  type SSEFrame,
} from "./sse";

describe("SSE transport", () => {
  it("parses UTF-8, CRLF at every possible byte boundary and multiline data", () => {
    const input =
      "\uFEFF: heartbeat\r\nid: 2\r\nevent: message.completed\r\ndata: 第一行\r\ndata: 第二行\r\n\r\n";
    const bytes = new TextEncoder().encode(input);
    for (let split = 0; split <= bytes.length; split++) {
      const frames: SSEFrame[] = [];
      const parser = new SSEParser((frame) => frames.push(frame));
      const decoder = new TextDecoder();
      parser.feed(decoder.decode(bytes.slice(0, split), { stream: true }));
      parser.feed(decoder.decode(bytes.slice(split)), true);
      expect(frames).toEqual([
        { id: "2", event: "message.completed", data: "第一行\n第二行" },
      ]);
    }
  });
  it("does not publish partial frames and handles bare CR and LF", () => {
    const frames: SSEFrame[] = [];
    const parser = new SSEParser((frame) => frames.push(frame));
    parser.feed("id: 1\ndata: a\n\ndata: b\r\rdata: unfinished", true);
    expect(frames.map((frame) => frame.data)).toEqual(["a", "b"]);
  });
  it("rejects wrong run, invalid payload and sequence gaps; deduplicates replay", () => {
    const id = "12345678-1234-1234-1234-123456789abc";
    const frame = {
      id: "1",
      event: "run.started",
      data: JSON.stringify({
        schema_version: "1",
        run_id: id,
        seq: 1,
        type: "run.started",
        occurred_at: "2026-11-02T08:00:00Z",
        payload: { status: "running", run_version: 2 },
      }),
    };
    expect(decodeRunEvent(frame, id).seq).toBe(1);
    expect(() => decodeRunEvent(frame, "other")).toThrow();
    expect(() =>
      decodeRunEvent(
        { ...frame, data: frame.data.replace('"running"', '"invented"') },
        id,
      ),
    ).toThrow();
    expect(acceptSequence(2, 2)).toBe(2);
    expect(acceptSequence(2, 1)).toBe(2);
    expect(acceptSequence(2, 3)).toBe(3);
    expect(() => acceptSequence(2, 4)).toThrow();
  });
});
