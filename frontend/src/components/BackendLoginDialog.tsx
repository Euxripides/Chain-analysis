import { Lock } from "lucide-react";
import { backendLoginHref } from "../auth/AdminSession";

/**
 * 后台会话缺失时的阻断弹窗（R-3 导航修复）。
 *
 * 项目规范：所有警告/说明类提示只能使用弹窗，禁止把横幅或说明段落嵌进页面正文。
 * 这里同时是**导航**修复：旧 V1 页面不会在缺少 admin 会话时假装后台功能可用，
 * 而是明确告知并提供通往后台入口 `/admin/login` 的路径。
 *
 * 注意：这不是权限绕过，也没有放宽任何服务端判定——服务端对这些接口
 * 仍然只会对 admin 受众会话放行（Policy 引擎决定）。
 */
export default function BackendLoginDialog({ returnTo }: { returnTo: string }) {
  const target = backendLoginHref(returnTo, "");
  return (
    <div className="notice-backdrop" role="presentation">
      <section
        className="notice-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="backend-login-title"
        aria-describedby="backend-login-message"
        data-testid="backend-login-dialog"
      >
        <header>
          <span>
            <Lock size={18} aria-hidden="true" />
          </span>
          <div>
            <small>后台访问边界</small>
            <h2 id="backend-login-title">需要管理员后台会话</h2>
          </div>
        </header>
        <p id="backend-login-message">
          该页面属于后台管理面（API 控制中心 / 状态总览），调用的是 <code>admin:*</code>{" "}
          权限接口。用户层会话无法访问后台接口，请先通过后台入口登录以取得 admin
          受众的独立会话。
        </p>
        <footer>
          <a className="primary" href={target}>
            去后台登录
          </a>
        </footer>
      </section>
    </div>
  );
}
