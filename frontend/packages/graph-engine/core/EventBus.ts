/**
 * 类型化事件总线（用户端重构规范 V4 §30.2 `core/EventBus`）
 *
 * 引擎与 React 之间**只通过事件通信**：图形渲染不经过 React，
 * 因此"选中了哪个节点""相机移动到哪里"必须有一条独立的、非渲染的通路。
 *
 * 约定：listener 抛出的异常不会打断其他 listener——一个订阅者的 bug
 * 不应该让整个图谱停摆。
 */
export type Listener<T> = (payload: T) => void;

export class EventBus<Events extends Record<string, unknown>> {
  private listeners = new Map<keyof Events, Set<Listener<never>>>();

  on<K extends keyof Events>(type: K, listener: Listener<Events[K]>): () => void {
    const set = this.listeners.get(type) ?? new Set();
    set.add(listener as Listener<never>);
    this.listeners.set(type, set);
    return () => this.off(type, listener);
  }

  off<K extends keyof Events>(type: K, listener: Listener<Events[K]>): void {
    this.listeners.get(type)?.delete(listener as Listener<never>);
  }

  emit<K extends keyof Events>(type: K, payload: Events[K]): void {
    const set = this.listeners.get(type);
    if (!set) return;
    for (const listener of [...set]) {
      try {
        (listener as Listener<Events[K]>)(payload);
      } catch (error) {
        /* 订阅者自身的问题不应该扩散成引擎故障。 */
        console.error("[graph-engine] listener failed", type, error);
      }
    }
  }

  clear(): void {
    this.listeners.clear();
  }
}
