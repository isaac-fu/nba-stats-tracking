-- Add source identity to the existing serving metadata view. Append columns so
-- CREATE OR REPLACE preserves the existing view contract and dependencies.
CREATE OR REPLACE VIEW api.release_metadata AS
SELECT r.release_id, r.status, r.first_published_at, r.source_cutoff,
       r.metric_contract_version,
       r.first_published_at AS published_at,
       r.metric_contract_version AS metrics_definition_version,
       observations.source_observed_at,
       b.details->>'source_name' AS source_name,
       coalesce((b.details->>'sample_data')::boolean, false) AS sample_data
FROM meta.release r
JOIN meta.pipeline_run b ON b.run_id = r.build_run_id
LEFT JOIN LATERAL (
    SELECT max(s.fetched_at) AS source_observed_at
    FROM meta.release_input i
    JOIN raw.page_snapshot s ON s.page_snapshot_id = i.page_snapshot_id
    WHERE i.release_id = r.release_id
) observations ON true
WHERE r.first_published_at IS NOT NULL;
