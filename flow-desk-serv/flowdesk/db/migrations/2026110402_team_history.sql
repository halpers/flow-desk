-- +goose Up
-- Old action receipts and prepared note actions identify exact ticket resources.
INSERT INTO run_ticket_resources(id,tenant_id,run_id,ticket_id)
SELECT gen_random_uuid(),a.tenant_id,a.run_id,r.business_object_id
FROM action_receipts r JOIN actions a ON a.tenant_id=r.tenant_id AND a.id=r.action_id
WHERE r.business_object_type='ticket'
ON CONFLICT(tenant_id,run_id,ticket_id) DO NOTHING;
INSERT INTO run_ticket_resources(id,tenant_id,run_id,ticket_id)
SELECT gen_random_uuid(),a.tenant_id,a.run_id,t.id
FROM actions a JOIN tickets t ON t.tenant_id=a.tenant_id AND t.id::text=a.arguments->>'ticket_id'
WHERE a.tool_name='ticket.add_note'
ON CONFLICT(tenant_id,run_id,ticket_id) DO NOTHING;
-- Legacy search events did not persist the returned IDs. Conservatively bind
-- those runs to every ticket that exists at upgrade time in the same tenant.
INSERT INTO run_ticket_resources(id,tenant_id,run_id,ticket_id)
SELECT gen_random_uuid(),r.tenant_id,r.run_id,t.id
FROM (SELECT DISTINCT tenant_id,run_id FROM run_events
      WHERE type IN ('tool.started','tool.completed') AND payload->>'tool_name'='ticket.search') r
JOIN tickets t ON t.tenant_id=r.tenant_id
ON CONFLICT(tenant_id,run_id,ticket_id) DO NOTHING;

-- +goose Down
-- Access provenance is retained when rolling back application images.
SELECT 1;
