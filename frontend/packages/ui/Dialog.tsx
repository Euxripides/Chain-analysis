import { makeDialogProvider } from "./dialog-core";
import { LegacyDialogRenderer } from "./dialog-legacy-renderer";

/**
 * `@chain/ui/Dialog` —— 兼容入口（用户端重构规范 V4 §21.0）
 *
 * 拆分之后本文件**只做转发**，保证：
 *   - `packages/ui/index.ts` 的 `export * from "./Dialog"` 不需要改动；
 *   - 后台端与用户端所有 `import { useDialog } from "@chain/ui"` 一行都不用改；
 *   - `DialogProvider` 默认仍然是旧 renderer，**后台端形态与行为零变化**。
 *
 * 需要用户端视觉时，由 main.tsx 显式传 `renderer={UserDialogRenderer}`；
 * 也可以在 `@chain/ui/dialog-core` 里直接拿到 Core 与两个 renderer。
 */
export * from "./dialog-core";
export { LegacyDialogRenderer } from "./dialog-legacy-renderer";

/** 默认走后台端 renderer；用户端在 main.tsx 覆盖。 */
export const DialogProvider = makeDialogProvider(LegacyDialogRenderer);
