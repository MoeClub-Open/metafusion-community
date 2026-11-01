"use client";

import { useTheme } from "@/lib/themeContext";
import { ThemePicker } from "./ThemePicker";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n/provider";
import { localeLabels, locales, type Locale } from "@/lib/i18n/routing";
import { useSession } from "@/lib/session-context";
import { describeError, formatLocaleList } from "@/lib/errors";
import { LOGIN_PATH, signInHrefOnce } from "@/lib/sign-in";
import { COMMUNITY_BOARD_MANAGE, COMMUNITY_POST_MODERATE, COMMUNITY_REPORT_REVIEW, COMMUNITY_TOPIC_PIN } from "@/lib/permissions";
import { BoardsPanel } from "./BoardsPanel";
import { TopicsPanel } from "./TopicsPanel";
import { PostsPanel } from "./PostsPanel";
import { ReportsPanel } from "./ReportsPanel";
import { NotificationsOutboxPanel } from "./NotificationsOutboxPanel";
import { FavoritesPanel } from "./FavoritesPanel";
import { Button, Card, Notice } from "./ui";
import { connectAdminWorkspace, prepareAdminNavigation, isAdminWorkspaceEmbedded, announceAdminNavigation, requestAdminWorkspaceLogin } from "@/lib/adminWorkspace";
import { ThemeModeSwitcher } from "./ThemeModeSwitcher";

type TabId = "boards" | "topics" | "posts" | "reports" | "notifications" | "favorites";
const TABS: { id: TabId; labelKey: string; codes: string[] }[] = [
  { id: "reports", labelKey: "admin.tab.reports", codes: [COMMUNITY_REPORT_REVIEW] },
  { id: "topics", labelKey: "admin.tab.topics", codes: [COMMUNITY_TOPIC_PIN, COMMUNITY_POST_MODERATE] },
  { id: "posts", labelKey: "admin.tab.posts", codes: [COMMUNITY_POST_MODERATE] },
  { id: "boards", labelKey: "admin.tab.boards", codes: [COMMUNITY_BOARD_MANAGE] },
  { id: "notifications", labelKey: "admin.tab.notifications", codes: [COMMUNITY_REPORT_REVIEW] },
  { id: "favorites", labelKey: "admin.tab.favorites", codes: [COMMUNITY_REPORT_REVIEW] },
];

function readHash(): string {
  if (typeof window === "undefined") return "";
  return window.location.hash.replace(/^#/, "");
}

export function AppShell() {
  const { t, locale, setLocale } = useI18n();
  const { status, user, error, reload, can } = useSession();
  const { setTheme } = useTheme();
  const [embedded, setEmbedded] = useState(false);
  useEffect(() => {
    const disconnect = connectAdminWorkspace(setLocale, setTheme);
    setEmbedded(disconnect !== null);
    return disconnect ?? undefined;
  }, [setLocale, setTheme]);
  const [tab, setTab] = useState<TabId | "">("");
  const [signInHref, setSignInHref] = useState<string>(LOGIN_PATH);

  useEffect(() => { setSignInHref(signInHrefOnce()); }, []);
  useEffect(() => { if (status === "anonymous") requestAdminWorkspaceLogin(); }, [status]);
  const allowed = useMemo(() => {
    const map: Record<string, boolean> = {};
    for (const item of TABS) map[item.id] = item.codes.some((code) => can(code));
    return map;
  }, [can]);

  useEffect(() => {
    const apply = () => {
      const wanted = readHash() as TabId;
      if (wanted && allowed[wanted]) setTab(wanted);
      else setTab(TABS.find((item) => allowed[item.id])?.id ?? "");
    };
    apply();
    window.addEventListener("hashchange", apply);
    window.addEventListener("popstate", apply);
    return () => { window.removeEventListener("hashchange", apply); window.removeEventListener("popstate", apply); };
  }, [allowed]);

  const selectTab = useCallback((next: TabId) => {
    setTab(next);
    if (typeof window !== "undefined" && window.location.hash !== `#${next}`) {
      prepareAdminNavigation(window.location.pathname + window.location.search + `#${next}`);
      if (isAdminWorkspaceEmbedded()) window.history.replaceState(window.history.state, "", `#${next}`);
      else window.history.pushState(window.history.state, "", `#${next}`);
    }
    announceAdminNavigation();
  }, []);
  const visibleTabs = TABS.filter((item) => allowed[item.id]);
  const anyAllowed = visibleTabs.length > 0;

  return (
    <div className="flex min-h-screen flex-col bg-background text-text-strong">
      <header hidden={embedded} className="sticky top-0 z-30 border-b border-line bg-surface/95 backdrop-blur">
        <div className="mx-auto flex h-14 w-full max-w-[80rem] items-center justify-between gap-2 px-4 sm:px-6">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <a href="/admin" aria-label={t("admin.backToHub")} title={t("admin.backToHub")} className="inline-flex h-11 w-11 sm:w-auto shrink-0 items-center justify-center gap-1 text-xs text-text-muted hover:text-text-strong"><span aria-hidden="true">←</span><span className="hidden sm:inline">{t("admin.backToHub")}</span></a>
            <span className="hidden text-text-muted sm:inline">/</span>
            <h1 className="min-w-0 truncate text-sm font-semibold text-text-strong">{t("admin.appTitle")}</h1>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <LocaleMenu />
            <ThemeModeSwitcher />
            <ThemePicker />
            {user ? <span className="hidden max-w-[9rem] truncate rounded border border-primary/30 bg-primary/15 px-2 py-0.5 font-mono text-xs text-primary sm:inline-flex" title={user.username}>{user.username}</span> : null}
          </div>
        </div>
      </header>

      <div className="mx-auto flex w-full max-w-[80rem] flex-1 flex-col gap-6 px-4 py-6 sm:px-6 lg:flex-row">
        {status === "ready" && anyAllowed ? (
          <aside className="w-full shrink-0 lg:w-52">
            <div className="rounded-xl border border-line bg-background p-3 lg:hidden">
              <label htmlFor="community-admin-section" className="mb-2 block text-xs font-medium text-text-muted">{t("admin.navLabel")}</label>
              <select id="community-admin-section" value={tab} onChange={(e) => selectTab(e.target.value as TabId)} className="w-full rounded-lg border border-line bg-background px-3 py-2.5 text-sm text-text-strong">
                {visibleTabs.map((item) => <option key={item.id} value={item.id}>{t(item.labelKey)}</option>)}
              </select>
            </div>
            <nav aria-label={t("admin.navLabel")} className={`sticky ${embedded ? "top-6" : "top-24"} hidden rounded-xl border border-line bg-background p-2 lg:block`}>
              {visibleTabs.map((item) => (
                <button key={item.id} type="button" aria-current={tab === item.id ? "page" : undefined} onClick={() => selectTab(item.id)} className={`w-full rounded-lg px-3 py-2.5 text-left text-sm transition-colors ${tab === item.id ? "bg-primary/15 font-semibold text-text-strong" : "text-text-muted hover:bg-primary/10 hover:text-text-strong"}`}>
                  {t(item.labelKey)}
                </button>
              ))}
            </nav>
          </aside>
        ) : null}

        <main className="min-w-0 flex-1 space-y-4">
          {status === "loading" ? <Card title={t("admin.session.title")}><p className="text-xs text-text-muted">{t("admin.loading")}</p></Card> : null}
          {status === "anonymous" ? (
            <Card title={t("admin.session.title")} desc={t("admin.session.signInHint")}>
              <a className="text-xs" href={signInHref}>{t("admin.session.goSignIn")}</a>
            </Card>
          ) : null}
          {status === "error" && error ? (
            <Card title={t("admin.session.title")}>
              <Notice kind="err" text={t("admin.session.loadFailed", { message: describeError(error, t, locale) })} />
              <div className="mt-3"><Button type="button" onClick={reload}>{t("admin.retry")}</Button></div>
            </Card>
          ) : null}
          {status === "ready" && !anyAllowed ? (
            <Card title={t("admin.denied.title")} desc={t("admin.denied.serviceNote")}>
              <Notice kind="err" text={t("admin.denied.body", { codes: formatLocaleList(TABS.flatMap((item) => item.codes), locale) })} />
            </Card>
          ) : null}
          {status === "ready" && anyAllowed && tab === "boards" && allowed.boards ? <BoardsPanel /> : null}
          {status === "ready" && anyAllowed && tab === "topics" && allowed.topics ? <TopicsPanel /> : null}
          {status === "ready" && anyAllowed && tab === "posts" && allowed.posts ? <PostsPanel /> : null}
          {status === "ready" && anyAllowed && tab === "reports" && allowed.reports ? <ReportsPanel /> : null}
          {status === "ready" && anyAllowed && tab === "notifications" && allowed.notifications ? <NotificationsOutboxPanel /> : null}
          {status === "ready" && anyAllowed && tab === "favorites" && allowed.favorites ? <FavoritesPanel /> : null}
        </main>
      </div>

    </div>
  );
}

function LocaleMenu() {
  const { t, locale, setLocale } = useI18n();
  const [open, setOpen] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onPointer = (event: MouseEvent) => { if (!container.current?.contains(event.target as Node)) setOpen(false); };
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onPointer); document.removeEventListener("keydown", onKey); };
  }, [open]);
  return <div className="relative" ref={container}>
    <button type="button" aria-label={t("admin.localeLabel")} title={t("admin.localeLabel")} aria-expanded={open} aria-haspopup="menu" onClick={() => setOpen(!open)} className="grid h-9 w-9 place-items-center rounded-control border border-line bg-surface text-text-strong hover:border-primary"><span aria-hidden="true">文</span></button>
    {open ? <div role="menu" aria-label={t("admin.localeLabel")} className="absolute right-0 z-50 mt-2 w-44 rounded-xl border border-line bg-surface p-1.5 shadow-xl">
      {locales.map((code) => <button key={code} type="button" role="menuitemradio" aria-checked={locale === code} onClick={() => { setLocale(code as Locale); setOpen(false); }} className={"flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-xs hover:bg-primary/10 " + (locale === code ? "font-semibold text-primary" : "text-text-strong")}>{localeLabels[code]}{locale === code ? "✓" : null}</button>)}
    </div> : null}
  </div>;
}
