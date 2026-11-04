-- +goose Up
ALTER TABLE actions DROP CONSTRAINT actions_tool_name_check;
ALTER TABLE actions ADD CONSTRAINT actions_tool_name_check
 CHECK(tool_name IN ('ticket.create','ticket.add_note','ticket.start_processing','ticket.resolve'));

-- +goose Down
-- Keep new immutable action history readable when rolling back application images.
SELECT 1;
