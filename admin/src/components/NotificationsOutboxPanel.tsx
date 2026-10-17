"use client";

import { useEffect, useState } from "react";
import { fetchNotificationsOutbox, retryDueNotificationsOutbox, type NotificationOutboxItem, type NotificationOutboxRetryResult, type NotificationOutboxStatus } from "@/lib/api/notifications-outbox";
import { describeError, toErrorLike } from "@/lib/errors";
import { formatDateTime, formatNumber } from "@/lib/format";
import { useI18n } from "@/lib/i18n/provider";
import { COMMUNITY_REPORT_REVIEW } from "@/lib/permissions";
import { useSession } from "@/lib/session-context";
import { Badge, Button, Card, CELL_CLASS, EmptyRow, Field, HEAD_CLASS, LoadingRow, Modal, Notice, Pagination, Select } from "./ui";

const STATUSES: NotificationOutboxStatus[] = ["pending", "sent", "failed", "expired"];

export function NotificationsOutboxPanel({ reloadKey }: { reloadKey: number }) {
  const { t, locale } = useI18n();
  const { can } = useSession();
  const allowed = can(COMMUNITY_REPORT_REVIEW);
  const [status, setStatus] = useState<NotificationOutboxStatus | "">("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [rows, setRows] = useState<NotificationOutboxItem[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [retrying, setRetrying] = useState(false);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [retryResult, setRetryResult] = useState<NotificationOutboxRetryResult | null>(null);
  const [nonce, setNonce] = useState(0);
  const [detail, setDetail] = useState<NotificationOutboxItem | null>(null);
  const pages = Math.max(1, Math.ceil(total / pageSize));

  useEffect(() => {
    if (!allowed) return;
    let alive = true;
    setLoading(true);
    setError("");
    fetchNotificationsOutbox({ status, page, pageSize })
      .then((data) => {
        if (!alive) return;
        setTotal(data.total);
        const lastPage = Math.max(1, Math.ceil(data.total / pageSize));
        if (page > lastPage) { setPage(lastPage); return; }
        setRows(data.items ?? []);
      })
      .catch((err: unknown) => {
        if (alive) setError(describeError(toErrorLike(err), t, locale, COMMUNITY_REPORT_REVIEW));
      })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [allowed, status, page, pageSize, nonce, reloadKey, t, locale]);

  async function retryDue() {
    if (!allowed || retrying) return;
    setRetrying(true);
    setActionError("");
    setRetryResult(null);
    try {
      const result = await retryDueNotificationsOutbox();
      setRetryResult(result);
      setNonce((value) => value + 1);
    } catch (err) {
      setActionError(describeError(toErrorLike(err), t, locale, COMMUNITY_REPORT_REVIEW));
    } finally { setRetrying(false); }
  }

  if (!allowed) return <Card title={t("admin.notifications.title")}>
    <Notice kind="err" text={t("admin.denied.body", { codes: COMMUNITY_REPORT_REVIEW })} />
  </Card>;

  return <Card title={t("admin.notifications.title")} desc={t("admin.notifications.desc")} actions={<>
    <Button type="button" disabled={loading || retrying} onClick={() => setNonce((value) => value + 1)}>{t("admin.refresh")}</Button>
    <Button type="button" variant="primary" disabled={retrying} onClick={retryDue}>{t(retrying ? "admin.notifications.retrying" : "admin.notifications.retryDue")}</Button>
  </>}>
    <div className="space-y-3">
      <div className="flex flex-wrap items-end gap-3">
        <Field label={t("admin.notifications.colStatus")}>
          <Select value={status} onChange={(event) => { setStatus(event.target.value as NotificationOutboxStatus | ""); setPage(1); }}>
            <option value="">{t("admin.notifications.allStatuses")}</option>
            {STATUSES.map((value) => <option key={value} value={value}>{t(`admin.notifications.status.${value}`)}</option>)}
          </Select>
        </Field>
        <Field label={t("admin.notifications.pageSize")}>
          <Select value={pageSize} onChange={(event) => { setPageSize(Number(event.target.value)); setPage(1); }}>
            {[20, 50, 100].map((value) => <option key={value} value={value}>{formatNumber(value, locale)}</option>)}
          </Select>
        </Field>
        <span className="pb-1 text-xs text-muted">{t("admin.total", { total: formatNumber(total, locale) })}</span>
      </div>
      <p className="text-xs leading-relaxed text-muted">{t("admin.notifications.retryHint")}</p>
      {retryResult ? <Notice kind="ok" text={t("admin.notifications.retried", { retried: formatNumber(retryResult.retried, locale), sent: formatNumber(retryResult.sent, locale), expired: formatNumber(retryResult.expired, locale) })} onClose={() => setRetryResult(null)} /> : null}
      {actionError ? <Notice kind="err" text={actionError} onClose={() => setActionError("")} /> : null}
      {error ? <Notice kind="err" text={error} /> : null}
      <div className="overflow-x-auto rounded-lg border border-line">
        <table className="w-full min-w-[44rem] border-collapse">
          <thead className="bg-surface"><tr>
            {["colNotification", "colRecipient", "colStatus", "colAttempts", "colNextRetry", "colError"].map((key) => <th key={key} className={HEAD_CLASS} scope="col">{t(`admin.notifications.${key}`)}</th>)}
            <th className={HEAD_CLASS} scope="col">{t("admin.actions")}</th>
          </tr></thead>
          <tbody className="divide-y divide-line">
            {loading ? <LoadingRow colSpan={7} text={t("admin.loading")} /> : error ? null : rows.length === 0 ? <EmptyRow colSpan={7} text={t("admin.notifications.empty")} /> : rows.map((row) => <tr key={row.id}>
              <td className={CELL_CLASS}><div className="font-mono">{row.type}</div><div className="mt-1 max-w-[14rem] truncate text-muted" title={row.id}>{row.id}</div><div className="mt-1 text-muted">{formatDateTime(row.created_at, locale)}</div></td>
              <td className={CELL_CLASS}><span className="break-all font-mono">{row.recipient_id}</span></td>
              <td className={CELL_CLASS}><Badge tone={row.status === "sent" ? "ok" : row.status === "failed" ? "warn" : row.status === "pending" ? "info" : "off"}>{t(`admin.notifications.status.${row.status}`)}</Badge></td>
              <td className={CELL_CLASS}>{formatNumber(row.attempts, locale)}</td>
              <td className={CELL_CLASS}>{row.status === "pending" ? formatDateTime(row.next_retry_at, locale) : "—"}</td>
              <td className={CELL_CLASS}><p className="max-w-[16rem] break-words">{row.last_error || "—"}</p></td>
              <td className={CELL_CLASS}><Button type="button" onClick={() => setDetail(row)}>{t("admin.notifications.detail")}</Button></td>
            </tr>)}
          </tbody>
        </table>
      </div>
      <Pagination page={page} pages={pages} onChange={setPage} disabled={loading || !!error} labels={{ prev: t("admin.prev"), next: t("admin.next"), info: t("admin.pageInfo", { page, pages }) }} />
    </div>
    {detail ? <Modal wide title={t("admin.notifications.detailTitle")} onClose={() => setDetail(null)}>
      <dl className="grid gap-3 text-xs sm:grid-cols-2">
        {[
          ["id", detail.id], ["colStatus", t(`admin.notifications.status.${detail.status}`)],
          ["colNotification", detail.type], ["colRecipient", detail.recipient_id],
          ["subject", `${detail.subject_type}: ${detail.subject_id}`], ["event", detail.event_id],
          ["actor", [detail.actor_name, detail.actor_id].filter(Boolean).join(" · ") || "—"], ["dedupeKey", detail.dedupe_key],
          ["colAttempts", formatNumber(detail.attempts, locale)], ["colNextRetry", detail.status === "pending" ? formatDateTime(detail.next_retry_at, locale) : "—"],
          ["createdAt", formatDateTime(detail.created_at, locale)], ["updatedAt", formatDateTime(detail.updated_at, locale)],
          ["expiresAt", formatDateTime(detail.expires_at, locale)], ["colError", detail.last_error || "—"],
        ].map(([key, value]) => <div key={key}><dt className="text-muted">{t(`admin.notifications.${key}`)}</dt><dd className="mt-1 break-all text-ink">{value}</dd></div>)}
      </dl>
      <details><summary className="cursor-pointer text-xs font-medium">{t("admin.notifications.payload")}</summary><pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-words rounded-lg border border-line bg-surface p-3 text-xs">{JSON.stringify(detail.payload, null, 2)}</pre></details>
    </Modal> : null}
  </Card>;
}
