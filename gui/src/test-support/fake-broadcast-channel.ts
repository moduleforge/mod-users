// A same-name in-process bus standing in for BroadcastChannel in tests: a
// message posted on one instance reaches every OTHER open instance of that
// name, asynchronously, like the real thing.

export class FakeBroadcastChannel {
  static open = new Map<string, Set<FakeBroadcastChannel>>();
  static posted: unknown[] = [];
  onmessage: ((event: MessageEvent) => void) | null = null;
  closed = false;

  constructor(public name: string) {
    const peers = FakeBroadcastChannel.open.get(name) ?? new Set();
    peers.add(this);
    FakeBroadcastChannel.open.set(name, peers);
  }

  static reset(): void {
    FakeBroadcastChannel.open.clear();
    FakeBroadcastChannel.posted = [];
  }

  postMessage(data: unknown): void {
    FakeBroadcastChannel.posted.push(data);
    for (const peer of FakeBroadcastChannel.open.get(this.name) ?? []) {
      if (peer === this || peer.closed) continue;
      queueMicrotask(() => {
        if (!peer.closed) peer.onmessage?.({ data } as MessageEvent);
      });
    }
  }

  close(): void {
    this.closed = true;
    FakeBroadcastChannel.open.get(this.name)?.delete(this);
  }
}
