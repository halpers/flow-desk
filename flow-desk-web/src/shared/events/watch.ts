import { api, authorizedFetch, refresh } from "../api/client";
import type { Models } from "../api/models";
import {
  SSEParser,
  acceptSequence,
  decodeRunEvent,
  type RunEvent,
} from "./sse";

export type Run = Models["RunData"];
export const terminal = (status: string) =>
  ["succeeded", "failed", "cancelled", "expired"].includes(status);
function delay(milliseconds: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) {
      reject(signal.reason);
      return;
    }
    const cancel = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", cancel);
      resolve();
    }, milliseconds);
    signal.addEventListener("abort", cancel, { once: true });
  });
}
export async function watchRun(
  runId: string,
  signal: AbortSignal,
  onSnapshot: (run: Run) => void,
  onEvent: (event: RunEvent) => void,
  onConnection: (state: string) => void,
) {
  let after = 0;
  let attempts = 0;
  while (!signal.aborted) {
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
    try {
      const snapshot = await api<Run>(`/runs/${runId}`, { signal });
      onSnapshot(snapshot);
      if (terminal(snapshot.status) && after >= snapshot.last_event_seq) {
        onConnection("已完成");
        return;
      }
      const response = await authorizedFetch(`/runs/${runId}/events`, {
        signal,
        headers: {
          Accept: "text/event-stream",
          "Last-Event-ID": String(after),
        },
      });
      if (response.status === 410 || response.status === 400) {
        after = snapshot.last_event_seq;
        continue;
      }
      if (
        !response.ok ||
        !response.headers.get("content-type")?.includes("text/event-stream") ||
        !response.body
      )
        throw new Error("进度连接暂时不可用");
      reader = response.body.getReader();
      onConnection("实时连接");
      attempts = 0;
      let authExpired = false;
      let needsSnapshot = false;
      const parser = new SSEParser((frame) => {
        if (frame.event === "auth.expired") {
          authExpired = true;
          return;
        }
        const event = decodeRunEvent(frame, runId);
        const next = acceptSequence(after, event.seq);
        if (next > after) {
          onEvent(event);
          after = next;
          if (
            event.type.startsWith("run.") ||
            [
              "message.completed",
              "approval.decided",
              "action.executed",
              "resource.revoked",
            ].includes(event.type)
          )
            needsSnapshot = true;
        }
      });
      const decoder = new TextDecoder();
      while (!signal.aborted) {
        const { done, value } = await reader.read();
        parser.feed(decoder.decode(value, { stream: !done }), done);
        if (needsSnapshot) {
          needsSnapshot = false;
          onSnapshot(await api<Run>(`/runs/${runId}`, { signal }));
        }
        if (done) break;
      }
      if (authExpired) await refresh();
      const latest = await api<Run>(`/runs/${runId}`, { signal });
      onSnapshot(latest);
      if (terminal(latest.status) && after >= latest.last_event_seq) {
        onConnection("已完成");
        return;
      }
    } catch {
      if (signal.aborted) return;
      onConnection("正在重连");
      attempts++;
      try {
        const latest = await api<Run>(`/runs/${runId}`, { signal });
        onSnapshot(latest);
        after = latest.last_event_seq;
      } catch {
        /* Preserve the last applied cursor until the API recovers. */
      }
    } finally {
      await reader?.cancel().catch(() => {});
      reader?.releaseLock();
    }
    try {
      await delay(Math.min(10000, 500 * 2 ** Math.min(attempts, 4)), signal);
    } catch {
      return;
    }
  }
}
