import { theme as antdTheme } from "antd";
import type { ThemeConfig } from "antd";
import { easing, durationMs } from "./motion";
import { palette, radius, shadow, softColors, textColors } from "./tokens";

/*
 * ChainSight Ant Design 主题（用户端重构规范 V4 §4.6）
 *
 * 原则（§1.3）：**使用 Ant Design 的能力，不使用它的默认长相。**
 * 因此这里逐项覆盖 global 与各组件 token，而不是在页面里写 `.ant-* !important`。
 *
 * 约定：
 *   - 色值只从 `./tokens` 取，本文件不出现裸十六进制；
 *   - 文字类 token 一律用 `textColors`（WCAG AA 安全变体），
 *     填充/描边类才用 `palette`（规范原始色）；
 *   - 默认算法固定为 defaultAlgorithm：本轮只交付浅色主题（§4.1）。
 */

const easeCss = `cubic-bezier(${easing.enter.join(", ")})`;

export const chainSightTheme: ThemeConfig = {
  algorithm: antdTheme.defaultAlgorithm,
  token: {
    /* --- 品牌与语义色 --- */
    colorPrimary: palette.primary,
    colorInfo: palette.primary,
    colorSuccess: palette.success,
    colorWarning: palette.warning,
    colorError: palette.danger,
    colorLink: textColors.primary,
    colorLinkHover: palette.primaryHover,
    colorLinkActive: palette.primaryActive,

    /* --- 文本：全部使用 AA 安全变体 --- */
    colorText: palette.textPrimary,
    colorTextSecondary: palette.textSecondary,
    colorTextTertiary: palette.textSecondary,
    colorTextQuaternary: palette.textMuted,
    colorTextHeading: palette.textPrimary,
    colorTextDescription: palette.textSecondary,
    colorTextPlaceholder: palette.textMuted,

    /* --- 背景 --- */
    colorBgLayout: palette.canvas,
    colorBgContainer: palette.surface,
    colorBgElevated: palette.surface,
    colorBgSpotlight: palette.textPrimary,
    colorBgTextHover: palette.primarySoft,
    colorBgTextActive: softColors.primary,
    colorBgMask: "rgba(16, 33, 61, 0.34)",

    /* --- 描边 --- */
    colorBorder: palette.border,
    colorBorderSecondary: palette.borderSubtle,
    colorSplit: palette.borderSubtle,
    controlOutline: "rgba(52, 120, 246, 0.18)",

    /* --- 圆角（§4.3） --- */
    borderRadius: radius.control,
    borderRadiusLG: radius.card,
    borderRadiusSM: 8,
    borderRadiusXS: 6,

    /* --- 字体（§5.1 / §5.2） --- */
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
    fontFamilyCode: '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", monospace',
    fontSize: 14,
    fontSizeSM: 12,
    fontSizeLG: 16,
    fontSizeHeading1: 32,
    fontSizeHeading2: 24,
    fontSizeHeading3: 18,
    fontSizeHeading4: 16,
    fontSizeHeading5: 14,
    lineHeight: 1.5,

    /* --- 动效（§9.1 / §9.2）：与 Motion 预设共用同一组时长 --- */
    motionDurationFast: `${durationMs.fast}ms`,
    motionDurationMid: `${durationMs.standard}ms`,
    motionDurationSlow: `${durationMs.spatial}ms`,
    motionEaseOut: easeCss,
    motionEaseInOut: `cubic-bezier(${easing.standard.join(", ")})`,

    /* --- 阴影：用规范 §4.4 的三档，而不是 Ant 默认 --- */
    boxShadow: shadow.surface,
    boxShadowSecondary: shadow.surfaceHover,
    boxShadowTertiary: shadow.surface,

    /* --- 控件尺寸（§8.2） --- */
    controlHeight: 40,
    controlHeightSM: 32,
    controlHeightLG: 44,
    wireframe: false,
  },
  components: {
    Button: {
      controlHeight: 40,
      controlHeightSM: 32,
      paddingInline: 16,
      fontWeight: 550,
      primaryShadow: "0 1px 2px rgba(45, 80, 130, .10)",
      defaultShadow: "none",
      dangerShadow: "none",
      /* 动画所有权归 CsButton（Motion press），关闭 Ant 自带的 Wave 涟漪动画（§9.6）。 */
      defaultBg: palette.surface,
      defaultBorderColor: palette.borderStrong,
      defaultColor: palette.textPrimary,
      colorPrimaryHover: palette.primaryHover,
      colorPrimaryActive: palette.primaryActive,
    },
    Input: {
      controlHeight: 40,
      paddingInline: 12,
      activeBorderColor: palette.primary,
      hoverBorderColor: palette.borderStrong,
      activeShadow: "0 0 0 3px rgba(52, 120, 246, .18)",
      errorActiveShadow: "0 0 0 3px rgba(216, 74, 74, .16)",
      colorTextPlaceholder: palette.textMuted,
    },
    InputNumber: {
      controlHeight: 40,
      activeBorderColor: palette.primary,
      hoverBorderColor: palette.borderStrong,
      activeShadow: "0 0 0 3px rgba(52, 120, 246, .18)",
    },
    Select: {
      controlHeight: 40,
      optionSelectedBg: palette.primarySoft,
      optionSelectedColor: textColors.primary,
      optionSelectedFontWeight: 600,
      optionActiveBg: palette.canvasStrong,
      optionHeight: 34,
    },
    TreeSelect: { controlHeight: 40, nodeSelectedBg: palette.primarySoft },
    Cascader: { controlHeight: 40, optionSelectedBg: palette.primarySoft },
    DatePicker: {
      controlHeight: 40,
      activeBorderColor: palette.primary,
      hoverBorderColor: palette.borderStrong,
      cellActiveWithRangeBg: palette.primarySoft,
    },
    Table: {
      headerBg: palette.canvasStrong,
      headerColor: palette.textSecondary,
      headerSplitColor: palette.border,
      rowHoverBg: palette.surfaceBlue,
      rowSelectedBg: palette.primarySoft,
      rowSelectedHoverBg: softColors.primary,
      borderColor: palette.borderSubtle,
      cellPaddingBlock: 11,
      cellPaddingInline: 16,
      cellFontSize: 13,
      footerBg: palette.surfaceBlue,
    },
    Tabs: {
      inkBarColor: palette.primary,
      itemColor: palette.textSecondary,
      itemHoverColor: textColors.primary,
      itemSelectedColor: textColors.primary,
      itemActiveColor: textColors.primary,
      horizontalItemGutter: 24,
      titleFontSize: 14,
    },
    Modal: {
      contentBg: palette.surface,
      headerBg: palette.surface,
      footerBg: "transparent",
      titleFontSize: 16,
      borderRadiusLG: radius.modal,
      paddingContentHorizontalLG: 24,
    },
    Drawer: {
      colorBgElevated: palette.surface,
      paddingLG: 24,
      footerPaddingBlock: 12,
    },
    Dropdown: {
      borderRadiusLG: radius.cardSm,
      controlItemBgHover: palette.surfaceBlue,
      controlItemBgActive: palette.primarySoft,
      paddingBlock: 6,
    },
    Popover: { borderRadiusLG: radius.cardSm, titleMinWidth: 160 },
    Tooltip: {
      colorBgSpotlight: palette.textPrimary,
      borderRadius: 8,
      fontSize: 12,
    },
    Form: {
      labelColor: palette.textPrimary,
      labelFontSize: 13,
      labelHeight: 22,
      itemMarginBottom: 20,
      verticalLabelPadding: "0 0 6px",
      labelRequiredMarkColor: palette.danger,
    },
    Progress: {
      defaultColor: palette.primary,
      remainingColor: palette.primarySoft,
      circleTextColor: palette.textPrimary,
      lineBorderRadius: 999,
    },
    Tag: {
      defaultBg: palette.surfaceBlue,
      defaultColor: palette.textSecondary,
      borderRadiusSM: 4,
    },
    Segmented: {
      itemSelectedBg: palette.surface,
      itemSelectedColor: textColors.primary,
      trackBg: palette.canvasStrong,
      trackPadding: 3,
    },
    Card: {
      colorBorderSecondary: palette.border,
      headerBg: "transparent",
      paddingLG: 22,
    },
    Pagination: { itemActiveBg: palette.primarySoft },
    Menu: {
      itemSelectedBg: palette.primarySoft,
      itemSelectedColor: textColors.primary,
      itemHoverBg: palette.canvasStrong,
      itemBorderRadius: radius.control,
      itemHeight: 36,
    },
    Layout: {
      bodyBg: palette.canvas,
      headerBg: palette.surfaceGlass,
      siderBg: palette.railSurface,
    },
  },
};

export default chainSightTheme;
