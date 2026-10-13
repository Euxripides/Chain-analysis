import { useEffect, useId, useRef, useState } from "react";
import { Input, Select, Space, Button } from "antd";
import type { InputRef } from "antd";
import { ArrowLeftRight, Box, Coins, Search, Wallet } from "lucide-react";
import { useApi } from "../../sprint5/common";
import { ChainIcon } from "./AssetIcon";
import { useExplorerLookup } from "./useExplorerLookup";

type ChainItem = { slug: string; name: string; enabled: boolean };
export function ExplorerSearch({ initialChain = "", initialValue = "", onOpen, variant = "default", chains, chainsLoading = false, lookupEngine }: { initialChain?: string; initialValue?: string; onOpen?: (chain: string, value: string, kind: "address" | "transaction" | "block" | "token") => void; variant?: "default" | "entry"; chains?: ChainItem[]; chainsLoading?: boolean; lookupEngine?: ReturnType<typeof useExplorerLookup> }) {
  const ownLookup = useExplorerLookup(onOpen);
  const { search: lookup, busy, cancel } = lookupEngine ?? ownLookup;
  const meta = useApi<{ items: ChainItem[] }>(chains === undefined ? "/api/v2/meta/chains" : null);
  const availableChains = chains ?? meta.data?.items ?? [];
  const id = useId();
  const inputRef = useRef<InputRef>(null);
  const [chain, setChain] = useState(initialChain); const [value, setValue] = useState(initialValue);
  useEffect(() => { if (initialChain) setChain(initialChain); }, [initialChain]);
  useEffect(() => { if (!chain) { const item = availableChains.find(item => item.enabled); if (item) setChain(item.slug); } }, [chain, chains, meta.data]);
  useEffect(() => {
    if (variant !== "entry") return;
    const focusInput = (event: KeyboardEvent) => {
      const target = event.target;
      if (event.key !== "/" || event.ctrlKey || event.altKey || event.metaKey || event.isComposing ||
        (target instanceof HTMLElement && target.closest("input, textarea, select, [contenteditable='true']")) ||
        document.querySelector(".ant-modal-wrap:not([style*='display: none']), [role='dialog']")) return;
      event.preventDefault(); inputRef.current?.focus();
    };
    document.addEventListener("keydown", focusInput);
    return () => document.removeEventListener("keydown", focusInput);
  }, [variant]);
  const search = (input: string) => lookup(chain, input);
  const changeChain = (next: string) => { cancel(); setChain(next); };
  const changeValue = (next: string) => { cancel(); setValue(next); };
  if (variant === "entry") return <form className="cs-entry-search" onSubmit={event => { event.preventDefault(); void search(value); }}>
    <div className="cs-entry-search__grid">
      <div className="cs-entry-search__field"><label htmlFor={`${id}-network`}>区块链网络</label><Select id={`${id}-network`} aria-label="选择链" value={chain || undefined} loading={chainsLoading} onChange={changeChain} options={availableChains.filter(item => item.enabled).map(item => ({ value: item.slug, label: <Space size={8}><ChainIcon chain={item.slug} />{item.name}</Space> }))} /></div>
      <div className="cs-entry-search__field cs-entry-search__input"><label htmlFor={`${id}-value`}>查询内容</label><Input id={`${id}-value`} ref={inputRef} aria-label="地址、交易哈希、区块或代币" value={value} onChange={event => changeValue(event.target.value)} onPressEnter={event => { if (event.nativeEvent.isComposing) event.preventDefault(); }} placeholder="输入地址、交易哈希、区块高度或代币" prefix={<Search size={18} aria-hidden="true" />} autoComplete="off" spellCheck={false} allowClear /></div>
      <Button className="cs-entry-search__submit" type="primary" htmlType="submit" loading={busy} icon={<Search size={18} aria-hidden="true" />}>查询</Button>
    </div>
    <div className="cs-entry-search__foot"><div className="cs-entry-supported" aria-label="支持的查询类型"><span>支持查询</span><span><Wallet size={14} aria-hidden="true" />地址</span><span><ArrowLeftRight size={14} aria-hidden="true" />交易</span><span><Box size={14} aria-hidden="true" />区块</span><span><Coins size={14} aria-hidden="true" />代币</span></div><div className="cs-entry-keys"><button type="button" aria-label="定位查询输入框" onClick={() => inputRef.current?.focus()}><kbd>/</kbd></button><button type="submit"><kbd>Enter</kbd> 查询</button></div></div>
  </form>;
  return <div className="cs-explorer-search">
    <Select aria-label="选择链" value={chain || undefined} loading={meta.loading} onChange={changeChain} options={availableChains.filter(item => item.enabled).map(item => ({ value: item.slug, label: <Space><ChainIcon chain={item.slug} />{item.name}</Space> }))} />
    <Input.Search aria-label="地址、交易哈希、区块或代币" value={value} onChange={event => changeValue(event.target.value)} placeholder="输入地址、交易哈希、区块或代币" enterButton="查询" onSearch={input => void search(input)} loading={busy} allowClear />
  </div>;
}
