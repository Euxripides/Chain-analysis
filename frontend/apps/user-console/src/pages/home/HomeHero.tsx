import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { motion, useMotionValue, useSpring, useTransform } from "motion/react";
import { ArrowRight, Network, Plus, Search } from "lucide-react";
import { GlassHeroCard, prefersReducedMotion } from "@chain/user-ui";

/**
 * 首页 Hero（用户端重构规范 V4 §32.2）
 *
 * 左：一句话价值主张 + 全局检索入口 + 三个主操作。
 * 右：**纯装饰**的抽象资金路径视觉（节点 / 方向 / 汇聚），
 *   **不含任何金额、地址、交易或进度数值**——规范 §64 #25 明确禁止为效果图虚构业务数据，
 *   因此这里与登录页的品牌面板采用同一手法：只表达结构，不标注任何值。
 *
 * 动效：
 *   - 背景 Aurora 22s 慢速漂移（§32.2）；
 *   - 鼠标视差 3–5px；
 *   - Reduced Motion 下全部静止（§9.5）。
 *
 * 检索行为：提交后跳 `/search?q=...`——这是 /search 页面**已经支持**的深链接参数
 * （SearchPage 里就有 useSearchParams().get("q")），不是本次新增的契约。
 * 不跳 Explorer：ExplorerPage 当前不接受查询参数，为它造一个就等于新增契约（§64 #1）。
 */

export interface HomeHeroProps {
  displayName: string;
  canTraceFunds: boolean;
  canCreateCase: boolean;
  /**
   * 数据时点 / 数据源 / 工作区等界面元信息（业务数据 + 字段标签，不属于"提示"）。
   *
   * 重构前这些信息放在 PageHeader 的 meta 里，**另外**还有一张"工作区状态"卡片
   * 重复展示同样的三个字段。规范 §32.4 要求不生成空壳大卡，因此合并到 Hero 下方一行。
   */
  meta?: React.ReactNode;
  /** 口径说明入口：说明内容必须走弹窗（AGENTS.md），所以这里只放触发按钮。 */
  onExplainScope?: () => void;
}

export function HomeHero({ displayName, canTraceFunds, canCreateCase, meta, onExplainScope }: HomeHeroProps) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const reduced = prefersReducedMotion();

  /* 鼠标视差用 MotionValue 而不是 state：装饰层跟随鼠标不应该触发 React 重渲染（§56.3）。 */
  const pointerX = useMotionValue(0);
  const pointerY = useMotionValue(0);
  const springX = useSpring(pointerX, { stiffness: 220, damping: 28, mass: 0.9 });
  const springY = useSpring(pointerY, { stiffness: 220, damping: 28, mass: 0.9 });
  const parallaxX = useTransform(springX, [-1, 1], [-5, 5]);
  const parallaxY = useTransform(springY, [-1, 1], [-3, 3]);

  const submit = () => {
    const q = query.trim();
    if (q.length < 2) return;
    navigate(`/search?q=${encodeURIComponent(q)}`);
  };

  return (
    <GlassHeroCard className="cs-homehero">
      <div
        className="cs-homehero__grid"
        onPointerMove={(event) => {
          if (reduced) return;
          const rect = event.currentTarget.getBoundingClientRect();
          pointerX.set(((event.clientX - rect.left) / rect.width) * 2 - 1);
          pointerY.set(((event.clientY - rect.top) / rect.height) * 2 - 1);
        }}
        onPointerLeave={() => {
          pointerX.set(0);
          pointerY.set(0);
        }}
      >
        <div className="cs-homehero__main">
          <h1 className="cs-homehero__title">
            从链上数据中，
            <br />
            发现真实资金关系
          </h1>
          <p className="cs-homehero__greet">{displayName ? `${displayName}，欢迎回来` : "欢迎回来"}</p>

          <form
            className="cs-homehero__search"
            role="search"
            onSubmit={(event) => {
              event.preventDefault();
              submit();
            }}
          >
            <label className="visually-hidden" htmlFor="home-hero-q">
              地址或交易哈希
            </label>
            <Search size={17} aria-hidden="true" className="cs-homehero__searchicon" />
            <input
              id="home-hero-q"
              className="cs-homehero__input"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="输入地址 / 交易哈希"
              autoComplete="off"
              spellCheck={false}
            />
            <button type="submit" className="cs-btn-hero" disabled={query.trim().length < 2}>
              开始调查
              <ArrowRight size={15} aria-hidden="true" />
            </button>
          </form>

          <div className="cs-homehero__actions">
            {canTraceFunds ? (
              <Link className="btn btn--sm" to="/graph">
                <Network size={14} aria-hidden="true" />
                资金追踪
              </Link>
            ) : null}
            {canCreateCase ? (
              <Link className="btn btn--ghost btn--sm" to="/cases/new">
                <Plus size={14} aria-hidden="true" />
                新建案件
              </Link>
            ) : null}
            {onExplainScope ? (
              <button type="button" className="btn btn--ghost btn--sm" onClick={onExplainScope}>
                口径说明
              </button>
            ) : null}
          </div>

          {meta ? <div className="cs-homehero__meta">{meta}</div> : null}
        </div>

        {/*
          装饰层：aria-hidden + 无任何数值。
          它不是"图谱预览"，也不代表任何真实数据；数据驱动的图谱在 /graph 与 /canvases/:id。
        */}
        <motion.div
          className="cs-homehero__art"
          aria-hidden="true"
          style={reduced ? undefined : { x: parallaxX, y: parallaxY }}
        >
          <svg viewBox="0 0 360 200" className="cs-homehero__svg" focusable="false">
            <defs>
              <linearGradient id="cs-homehero-line" x1="0" y1="0" x2="1" y2="0">
                <stop offset="0%" stopColor="#3478F6" stopOpacity="0.14" />
                <stop offset="55%" stopColor="#1DB8E8" stopOpacity="0.85" />
                <stop offset="100%" stopColor="#6A63FF" stopOpacity="0.22" />
              </linearGradient>
            </defs>
            <path className="cs-homehero__edge" d="M28 40 C 120 40, 140 100, 232 100" />
            <path className="cs-homehero__edge" d="M28 160 C 120 160, 140 100, 232 100" />
            <path className="cs-homehero__edge" d="M232 100 C 292 100, 300 56, 336 56" />
            <path className="cs-homehero__edge" d="M232 100 C 292 100, 300 148, 336 148" />
            <g className="cs-homehero__node" transform="translate(28 40)">
              <circle r="6" />
            </g>
            <g className="cs-homehero__node" transform="translate(28 160)">
              <circle r="6" />
            </g>
            <g className="cs-homehero__node cs-homehero__node--hub" transform="translate(232 100)">
              <rect x="-8" y="-8" width="16" height="16" rx="4" />
            </g>
            <g className="cs-homehero__node" transform="translate(336 56)">
              <polygon points="0,-8 8,0 0,8 -8,0" />
            </g>
            <g className="cs-homehero__node" transform="translate(336 148)">
              <polygon points="0,-8 8,0 0,8 -8,0" />
            </g>
          </svg>
        </motion.div>
      </div>
    </GlassHeroCard>
  );
}
