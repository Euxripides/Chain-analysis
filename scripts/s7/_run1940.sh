set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "
select ro.key,
       bool_or(rp.permission_key='admin:provider:read') as has_read,
       bool_or(rp.permission_key='admin:provider:update') as has_update,
       bool_or(rp.permission_key='admin:provider:key:rotate') as has_rotate
from roles ro join role_permissions rp on rp.role_id=ro.id
where ro.key in ('owner','platform_admin','security_admin','auditor','analyst','investigator','reviewer','viewer','api_client')
group by ro.key order by ro.key"
echo SCRIPT_END
