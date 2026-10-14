package api

import (
	"context"
	"fmt"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"strconv"
)

type contractFinalGuardKey struct{}

func (s *Server) contractBudgetContext(ctx context.Context, scope tenantScope, id string, q ca.Request) (context.Context, error) {
	_, err := s.deps.Store.Pool.Exec(ctx, `INSERT INTO contract_analysis_budgets(analysis_id,organization_id,workspace_id,max_requests,max_credits,max_state_reads) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(analysis_id) DO NOTHING`, id, scope.OrganizationID, scope.WorkspaceID, q.Budget.MaxRPCRequests, q.Budget.MaxCredits, q.Budget.MaxStateReads)
	if err != nil {
		return ctx, err
	}
	var valid bool
	err = s.deps.Store.Pool.QueryRow(ctx, `SELECT organization_id=$2::uuid AND workspace_id=$3::uuid AND max_requests=$4 AND max_credits=$5 AND max_state_reads=$6 FROM contract_analysis_budgets WHERE analysis_id=$1`, id, scope.OrganizationID, scope.WorkspaceID, q.Budget.MaxRPCRequests, q.Budget.MaxCredits, q.Budget.MaxStateReads).Scan(&valid)
	if err != nil {
		return ctx, err
	}
	if !valid {
		return ctx, fmt.Errorf("immutable analysis budget changed")
	}
	return provider.WithDownloadBudget(ctx, func(callCtx context.Context, calls int64, credits, cost float64) error {
		final, _ := callCtx.Value(contractFinalGuardKey{}).(bool)
		limit := q.Budget.MaxRPCRequests - 1
		if final {
			limit = q.Budget.MaxRPCRequests
		}
		amount := strconv.FormatFloat(credits*float64(calls), 'g', -1, 64)
		if calls < 1 || credits < 0 {
			return fmt.Errorf("invalid RPC reservation")
		}
		worker, _ := callCtx.Value(nextWorkerKey{}).(*nextWorkerState)
		owner := ""
		var fence int64
		if worker != nil {
			owner, fence = worker.Owner, worker.Fence
		}
		tag, err := s.deps.Store.Pool.Exec(callCtx, `UPDATE contract_analysis_budgets SET reserved_requests=reserved_requests+$2,reserved_credits=reserved_credits+$3::numeric,updated_at=now() WHERE analysis_id=$1 AND reserved_requests+$2<=$4 AND reserved_credits+$3::numeric<=max_credits AND ($6::bigint=0 OR EXISTS(SELECT 1 FROM investigation_analysis_jobs j WHERE j.id=$1 AND j.fence=$6 AND j.lease_owner=NULLIF($5,'')::uuid AND j.status='running' AND j.lease_until>now()))`, id, calls, amount, limit, owner, fence)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("contract RPC request or credit budget exhausted")
		}
		return nil
	}), nil
}
func (s *Server) contractReserveState(ctx context.Context) error {
	w, ok := ctx.Value(nextWorkerKey{}).(*nextWorkerState)
	if !ok || w.JobID == "" {
		return fmt.Errorf("contract state reservation requires task")
	}
	tag, err := s.deps.Store.Pool.Exec(ctx, `UPDATE contract_analysis_budgets SET state_reads=state_reads+1,updated_at=now() WHERE analysis_id=$1 AND state_reads<max_state_reads AND EXISTS(SELECT 1 FROM investigation_analysis_jobs j WHERE j.id=$1 AND j.fence=$2 AND j.lease_owner=$3 AND j.status='running' AND j.lease_until>now())`, w.JobID, w.Fence, w.Owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("contract state read budget exhausted")
	}
	return nil
}
