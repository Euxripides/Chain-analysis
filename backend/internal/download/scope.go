package download

import "github.com/blockchain-platform/blockchain-platform/internal/model"

// jobOrgID / jobWorkspaceID 读取下载任务自身的租户归属。
//
// 后台下载路径（runner / finalize）没有 HTTP 主体，但它处理的任务必然带有
// organization_id / workspace_id。元数据落库必须使用任务自己的作用域，
// 而不是 Store.DefaultOrgWorkspace（那是 P0 越权读取的成因之一）。
func jobOrgID(job *model.DownloadJob) string {
	if job == nil || job.OrganizationID == nil {
		return ""
	}
	return *job.OrganizationID
}

func jobWorkspaceID(job *model.DownloadJob) string {
	if job == nil || job.WorkspaceID == nil {
		return ""
	}
	return *job.WorkspaceID
}
