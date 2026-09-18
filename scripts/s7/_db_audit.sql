\pset pager off
select tablename, indexname, indexdef from pg_indexes
where schemaname='public' and tablename in ('download_pages','download_datasets','download_shards','download_subjects','organization_memberships','workspace_memberships','investigation_runs','review_requests','case_activity','dataset_coverage','token_metadata_observations')
order by tablename, indexname;
