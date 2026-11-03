"""Export authorized local Agent metadata without prompts, headers or reasoning."""

import argparse
import json

from eval_common import ROOT, agent_connection, settings

parser = argparse.ArgumentParser()
parser.add_argument("--report", required=True)
args = parser.parse_args()
source = (ROOT / args.report).resolve()
assert source.is_relative_to(ROOT / "reports/m5") and source.suffix == ".json"
report = json.loads(source.read_text(encoding="utf-8"))
traces = []
with agent_connection(settings()) as conn:
    for case in report["cases"]:
        if "run_id" not in case:
            continue
        run_id = case["run_id"]
        spans = conn.execute(
            "SELECT span_id,dispatch_id,lease_generation,node_name,status,duration_ms,completed_at FROM execution_spans WHERE run_id=%s ORDER BY completed_at,span_id",
            (run_id,),
        ).fetchall()
        models = conn.execute(
            "SELECT call_id,model,profile,status,error_code,input_tokens,output_tokens,duration_ms,cost_status FROM model_calls WHERE run_id=%s ORDER BY created_at,call_id",
            (run_id,),
        ).fetchall()
        traces.append(
            {
                "task_id": case["task_id"],
                "trace_id": run_id,
                "run_id": run_id,
                "request_ids": case.get("request_ids", []),
                "node_attempts": spans,
                "model_calls": models,
                "public_events": case.get("events", []),
            }
        )
output = source.with_name(source.stem + "-traces.json")
output.write_text(
    json.dumps(
        {
            "schema_version": "trace-v1",
            "profile": report["profile"],
            "trace_id_definition": "stable run_id; dispatch_id identifies a resume generation",
            "redaction": "metadata only; no prompts, model output, tool result bodies or credentials",
            "traces": traces,
        },
        ensure_ascii=False,
        indent=2,
        default=str,
    )
    + "\n",
    encoding="utf-8",
)
print(f"Exported {len(traces)} run traces to {output.relative_to(ROOT)}")
