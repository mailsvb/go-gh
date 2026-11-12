import { Events } from "@wailsio/runtime";
import { AuthService, EventService, RepoService } from "../../bindings/go-gh/internal/services";
import type { State } from "../../bindings/go-gh/internal/core";
import type { Event } from "../../bindings/go-gh/internal/events";
import type { Repo } from "../../bindings/go-gh/internal/gh";
import { EventsNew, EventsReset, ReposChanged, StateChanged } from "../../bindings/events";

export const app = $state<{
  state: State | null;
  repos: Repo[];
  events: Event[];
  unread: number;
}>({ state: null, repos: [], events: [], unread: 0 });

const MAX_EVENTS = 100;

let started = false;

export async function init(): Promise<void> {
  if (started) return;
  started = true;

  Events.On(StateChanged, (e) => {
    app.state = e.data;
  });
  Events.On(ReposChanged, (e) => {
    app.repos = e.data ?? [];
  });
  Events.On(EventsReset, (e) => {
    app.events = e.data ?? [];
  });
  Events.On(EventsNew, (e) => {
    if (app.events.some((x) => x.key === e.data.key)) return;
    app.events = [e.data, ...app.events].slice(0, MAX_EVENTS);
    app.unread++;
  });

  const [state, repos, events] = await Promise.all([
    AuthService.State(),
    RepoService.Repos(),
    EventService.Recent(),
  ]);
  app.state ??= state;
  if (app.repos.length === 0) app.repos = repos ?? [];
  if (app.events.length === 0) app.events = events ?? [];
}

export function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 45) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.round(h / 24);
  if (d < 30) return `${d}d ago`;
  return new Date(iso).toLocaleDateString();
}
