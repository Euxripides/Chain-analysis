-- Restore auditable Ankr estimates for TRON node calls recorded before pricing
-- was attached at the shared usage hook. One logical call = 700 credits = $0.00007.
UPDATE provider_usage
SET credits = 700 * GREATEST(method_calls, 1),
    estimated_cost_usd = 0.00007 * GREATEST(method_calls, 1)
WHERE provider = 'ankr'
  AND usage_type <> 'trc20_enrichment_summary'
  AND estimated_cost_usd = 0;
