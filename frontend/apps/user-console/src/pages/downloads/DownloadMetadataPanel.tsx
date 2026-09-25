import React, { useEffect, useMemo, useRef, useState } from "react";
import { Select } from "antd";
import { AnimatePresence, motion } from "motion/react";
import { ChevronDown, LoaderCircle, Plus, Tags } from "lucide-react";
import { useDialog } from "@chain/ui";
import type { ImportedAddress } from "../../import/parse-core";
import { DL_EASE_ENTER, DL_EASE_EXIT, DL_EXIT_RATIO, downloadMotion } from "./download.motion";

export interface DownloadMetadataOption {
  id: string;
  name: string;
  color?: string;
}

export interface DownloadMetadataPanelProps {
  addresses: ImportedAddress[];
  defaultChain: string;
  cases: DownloadMetadataOption[];
  tags: DownloadMetadataOption[];
  caseName: string;
  defaultTags: string[];
  loadingCases: boolean;
  loadingTags: boolean;
  casesError: string | null;
  tagsError: string | null;
  busy: boolean;
  canCreateCase: boolean;
  canCreateTag: boolean;
  onCaseNameChange: (value: string) => void;
  onDefaultTagsChange: (values: string[]) => void;
  onAddressTagsChange: (chain: string, address: string, values: string[]) => void;
  onOpen: () => void;
  onCreateCase: () => void;
  onCreateTag: () => void;
  onReloadCases: () => void;
  onReloadTags: () => void;
  onTagLimitReached: () => void;
}

const MAX_VISIBLE_ADDRESSES = 6;
const MAX_TAGS = 20;

function compactAddress(value: string): string {
  if (value.length <= 26) return value;
  return value.slice(0, 14) + "…" + value.slice(-8);
}

function MetadataSelect({
  id,
  ariaLabel,
  className,
  options,
  value,
  loading,
  placeholder,
  multiple = false,
  onChange,
}: {
  id: string;
  ariaLabel: string;
  className?: string;
  options: DownloadMetadataOption[];
  value: string | string[];
  loading: boolean;
  placeholder: string;
  multiple?: boolean;
  onChange: (next: string | string[]) => void;
}) {
  const items = useMemo(
    () => options.map((item) => ({ value: item.name, label: item.name })),
    [options],
  );

  if (multiple) {
    return (
      <Select<string[]>
        id={id}
        aria-label={ariaLabel}
        className={`dl-meta-select dl-meta-select--multiple ${className ?? ""}`.trim()}
        mode="multiple"
        value={Array.isArray(value) ? value : []}
        options={items}
        loading={loading}
        disabled={loading}
        showSearch
        allowClear
        optionFilterProp="label"
        placeholder={placeholder}
        classNames={{ popup: { root: "cs-select-dropdown-wizard dl-meta-select-dropdown" } }}
        popupMatchSelectWidth={false}
        listHeight={280}
        onChange={(next) => onChange(next)}
      />
    );
  }

  return (
    <Select<string>
      id={id}
      aria-label={ariaLabel}
      className={`dl-meta-select ${className ?? ""}`.trim()}
      value={typeof value === "string" && value ? value : undefined}
      options={items}
      loading={loading}
      disabled={loading}
      showSearch
      allowClear
      optionFilterProp="label"
      placeholder={placeholder}
      classNames={{ popup: { root: "cs-select-dropdown-wizard dl-meta-select-dropdown" } }}
      popupMatchSelectWidth={false}
      listHeight={280}
      onChange={(next) => onChange(next ?? "")}
      onClear={() => onChange("")}
    />
  );
}

/**
 * 下载任务高级设置：任务级案件/标签 + 每个地址的标签覆盖。
 *
 * 面板默认收起，不改变主流程密度；展开后所有字段都是真实业务输入。
 * 地址标签留空表示沿用任务默认标签，非空时写入 subject_meta 并覆盖默认值。
 */
export function DownloadMetadataPanel({
  addresses,
  defaultChain,
  cases,
  tags,
  caseName,
  defaultTags,
  loadingCases,
  loadingTags,
  casesError,
  tagsError,
  busy,
  canCreateCase,
  canCreateTag,
  onCaseNameChange,
  onDefaultTagsChange,
  onAddressTagsChange,
  onOpen,
  onCreateCase,
  onCreateTag,
  onReloadCases,
  onReloadTags,
  onTagLimitReached,
}: DownloadMetadataPanelProps) {
  const dialog = useDialog();
  const [open, setOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const notifiedErrors = useRef(new Set<string>());

  useEffect(() => {
    const entries = [
      casesError ? { key: "cases:" + casesError, title: "案件列表加载失败", body: casesError, reload: onReloadCases } : null,
      tagsError ? { key: "tags:" + tagsError, title: "标签列表加载失败", body: tagsError, reload: onReloadTags } : null,
    ].filter((item): item is { key: string; title: string; body: string; reload: () => void } => Boolean(item));
    for (const item of entries) {
      if (notifiedErrors.current.has(item.key)) continue;
      notifiedErrors.current.add(item.key);
      void (async () => {
        const answer = await dialog.open({
          kind: "error",
          title: item.title,
          body: item.body,
          actions: [
            { label: "关闭", result: "", variant: "default" },
            { label: "重新加载", result: "reload", variant: "primary" },
          ],
        });
        if (answer === "reload") {
          notifiedErrors.current.delete(item.key);
          item.reload();
        }
      })();
    }
  }, [casesError, dialog, onReloadCases, onReloadTags, tagsError]);

  useEffect(() => {
    if (addresses.length <= MAX_VISIBLE_ADDRESSES) setShowAll(false);
  }, [addresses.length]);

  const taggedAddressCount = addresses.filter((item) => item.tags.length > 0).length;
  const visibleAddresses = showAll ? addresses : addresses.slice(0, MAX_VISIBLE_ADDRESSES);
  const summary = [
    caseName ? "案件 1" : null,
    defaultTags.length > 0 ? `默认标签 ${defaultTags.length}` : null,
    taggedAddressCount > 0 ? `单独设置 ${taggedAddressCount}` : null,
  ].filter(Boolean).join(" · ") || "未设置";

  const acceptTags = (next: string[], commit: (values: string[]) => void) => {
    if (next.length > MAX_TAGS) {
      onTagLimitReached();
      return;
    }
    commit(next);
  };

  return (
    <div className="dl-meta" data-open={open ? "true" : undefined}>
      <button
        type="button"
        className="dl-meta__toggle"
        aria-expanded={open}
        aria-controls="download-metadata-body"
        onClick={() => {
          if (!open) onOpen();
          setOpen((value) => !value);
        }}
      >
        <span className="dl-meta__toggle-icon" aria-hidden="true"><Tags size={17} /></span>
        <span className="dl-meta__toggle-copy">
          <span className="dl-meta__toggle-title">高级设置</span>
          <span className="dl-meta__toggle-summary">{summary}</span>
        </span>
        <ChevronDown className="dl-meta__toggle-chevron" size={18} aria-hidden="true" />
      </button>

      <AnimatePresence initial={false}>
        {open ? (
          <motion.div
            id="download-metadata-body"
            className="dl-meta__body"
            initial={{ opacity: 0, y: -6 }}
            animate={{ opacity: 1, y: 0, transition: { duration: downloadMotion.standard, ease: DL_EASE_ENTER } }}
            exit={{ opacity: 0, y: -4, transition: { duration: downloadMotion.standard * DL_EXIT_RATIO, ease: DL_EASE_EXIT } }}
          >
            <div className="dl-meta__defaults">
              <div className="dl-meta__field">
                <div className="dl-meta__labelrow">
                  <label className="dl-field-label" htmlFor="download-case">任务默认案件</label>
                  {canCreateCase ? (
                    <button type="button" className="dl-meta__create" onClick={onCreateCase} disabled={busy}>
                      {busy ? <LoaderCircle className="dl-spin" size={13} aria-hidden="true" /> : <Plus size={13} aria-hidden="true" />}
                      新建案件
                    </button>
                  ) : null}
                </div>
                <MetadataSelect
                  id="download-case"
                  ariaLabel="任务默认案件"
                  className="dl-meta-select--case"
                  options={cases}
                  value={caseName}
                  loading={loadingCases}
                  placeholder="未关联案件"
                  onChange={(next) => onCaseNameChange(String(next))}
                />
              </div>

              <div className="dl-meta__field">
                <div className="dl-meta__labelrow">
                  <label className="dl-field-label" htmlFor="download-default-tags">任务默认标签</label>
                  {canCreateTag ? (
                    <button type="button" className="dl-meta__create" onClick={onCreateTag} disabled={busy}>
                      {busy ? <LoaderCircle className="dl-spin" size={13} aria-hidden="true" /> : <Plus size={13} aria-hidden="true" />}
                      新建标签
                    </button>
                  ) : null}
                </div>
                <MetadataSelect
                  id="download-default-tags"
                  ariaLabel="任务默认标签"
                  options={tags}
                  value={defaultTags}
                  loading={loadingTags}
                  placeholder="未设置默认标签"
                  multiple
                  onChange={(next) => acceptTags(next as string[], onDefaultTagsChange)}
                />
              </div>
            </div>

            {addresses.length > 0 ? (
              <section className="dl-meta__addresses" aria-labelledby="download-address-tags-title">
                <div className="dl-meta__subhead">
                  <span id="download-address-tags-title" className="dl-meta__subtitle">地址标签</span>
                  <span className="dl-meta__count">已单独设置 {taggedAddressCount} / {addresses.length}</span>
                </div>
                <div className="dl-meta__address-list">
                  {visibleAddresses.map((item, index) => {
                    const fullAddress = item.normalized || item.input;
                    return (
                      <div className="dl-meta__address-row" key={(item.chain || defaultChain) + ":" + fullAddress} data-address={fullAddress}>
                        <div className="dl-meta__address-copy">
                          <span className="dl-meta__address-index">地址 {index + 1}</span>
                          <span className="dl-meta__address-value mono" title={fullAddress}>{compactAddress(fullAddress)}</span>
                        </div>
                        <MetadataSelect
                          id={`download-address-tags-${index + 1}`}
                          ariaLabel={`地址 ${index + 1} 单独标签`}
                          className="dl-meta-select--address"
                          options={tags}
                          value={item.tags}
                          loading={loadingTags}
                          placeholder={defaultTags.length > 0 ? "使用任务默认标签" : "未设置标签"}
                          multiple
                          onChange={(next) => acceptTags(next as string[], (values) => onAddressTagsChange(item.chain || defaultChain, fullAddress, values))}
                        />
                      </div>
                    );
                  })}
                </div>
                {addresses.length > MAX_VISIBLE_ADDRESSES ? (
                  <button type="button" className="dl-meta__showall" onClick={() => setShowAll((value) => !value)}>
                    {showAll ? "收起地址列表" : `显示全部 ${addresses.length} 个地址`}
                  </button>
                ) : null}
              </section>
            ) : null}
          </motion.div>
        ) : null}
      </AnimatePresence>
    </div>
  );
}

export default DownloadMetadataPanel;
