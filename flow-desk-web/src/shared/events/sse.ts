import Ajv2020 from "ajv/dist/2020";
import addFormats from "ajv-formats";
import schema from "./run.schema.json";
import type { Models } from "../api/models";

export type RunEvent = Required<Models["EventData"]>;
export interface SSEFrame {
  id: string;
  event: string;
  data: string;
}
const ajv = new Ajv2020({ strict: false });
addFormats(ajv);
const validEvent = ajv.compile(schema);
const MAX_BUFFER = 1024 * 1024;

// Handles CRLF split across chunks, bare CR/LF, multiline data and comments.
export class SSEParser {
  private buffer = "";
  private id = "";
  private event = "";
  private data: string[] = [];
  private eventBytes = 0;
  private first = true;
  constructor(private emit: (frame: SSEFrame) => void) {}
  feed(chunk: string, eof = false) {
    if (this.first && chunk) {
      chunk = chunk.replace(/^\uFEFF/, "");
      this.first = false;
    }
    this.buffer += chunk;
    if (this.buffer.length > MAX_BUFFER) throw new Error("事件超过大小限制");
    let offset = 0;
    for (let index = 0; index < this.buffer.length; index++) {
      const char = this.buffer[index];
      if (char !== "\r" && char !== "\n") continue;
      if (char === "\r" && index === this.buffer.length - 1 && !eof) break;
      this.line(this.buffer.slice(offset, index));
      if (char === "\r" && this.buffer[index + 1] === "\n") index++;
      offset = index + 1;
    }
    this.buffer = this.buffer.slice(offset);
  }
  private line(line: string) {
    if (!line) {
      if (this.data.length)
        this.emit({
          id: this.id,
          event: this.event || "message",
          data: this.data.join("\n"),
        });
      this.data = [];
      this.event = "";
      this.eventBytes = 0;
      return;
    }
    if (line.startsWith(":")) return;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    const value = colon < 0 ? "" : line.slice(colon + 1).replace(/^ /, "");
    if (field === "data") {
      this.eventBytes += value.length;
      if (this.eventBytes > MAX_BUFFER) throw new Error("事件超过大小限制");
      this.data.push(value);
    }
    if (field === "id" && !value.includes("\0")) this.id = value;
    if (field === "event") this.event = value;
  }
}
export function decodeRunEvent(frame: SSEFrame, runId: string): RunEvent {
  const value: unknown = JSON.parse(frame.data);
  if (!validEvent(value)) throw new Error("事件契约校验失败");
  const event = value as RunEvent;
  if (
    event.run_id !== runId ||
    String(event.seq) !== frame.id ||
    event.type !== frame.event
  )
    throw new Error("事件标识不匹配");
  return event;
}
export function acceptSequence(current: number, incoming: number): number {
  if (incoming <= current) return current;
  if (incoming !== current + 1) throw new Error("事件序列不连续");
  return incoming;
}
