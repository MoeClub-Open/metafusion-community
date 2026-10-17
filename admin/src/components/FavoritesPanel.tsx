"use client";

import { useEffect, useState } from "react";
import { deleteAdminFavorite, fetchAdminFavorites, type AdminFavoriteRow, type FavoriteTargetType } from "@/lib/api/favorites";
import { describeError, toErrorLike } from "@/lib/errors";
import { formatDateTime, formatNumber } from "@/lib/format";
import { useI18n } from "@/lib/i18n/provider";
import { COMMUNITY_REPORT_REVIEW } from "@/lib/permissions";
import { useSession } from "@/lib/session-context";
import { Button, Card, CELL_CLASS, ConfirmDialog, EmptyRow, Field, HEAD_CLASS, LoadingRow, Notice, Pagination, Select, TextInput } from "./ui";

const TARGET_TYPES: FavoriteTargetType[] = ["agent", "collection", "work", "content_unit", "expression", "release", "medium", "track"];
const EMPTY_FILTERS = { ownerID: "", targetType: "" as FavoriteTargetType | "", targetID: "" };

function rowKey(row: AdminFavoriteRow): string { return `${row.owner_id}:${row.target_type}:${row.target_id}`; }

export function FavoritesPanel({ reloadKey }: { reloadKey: number }) {
  const { t, locale } = useI18n();
  const { can } = useSession();
  const allowed = can(COMMUNITY_REPORT_REVIEW);
  const [draft, setDraft] = useState(EMPTY_FILTERS);
  const [filters, setFilters] = useState(EMPTY_FILTERS);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [rows, setRows] = useState<AdminFavoriteRow[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [deleted, setDeleted] = useState(false);
  const [pending, setPending] = useState<AdminFavoriteRow | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [nonce, setNonce] = useState(0);
  const pages = Math.max(1, Math.ceil(total / pageSize));

  useEffect(() => {
    if (!allowed) return;
    let alive = true;
    setLoading(true);
    setError("");
    fetchAdminFavorites({ ...filters, page, pageSize })
      .then((data) => {
        if (!alive) return;
        setTotal(data.total);
        const lastPage = Math.max(1, Math.ceil(data.total / pageSize));
        if (page > lastPage) { setPage(lastPage); return; }
        setRows(data.items ?? []);
      })
      .catch((err: unknown) => { if (alive) setError(describeError(toErrorLike(err), t, locale, COMMUNITY_REPORT_REVIEW)); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [allowed, filters, page, pageSize, nonce, reloadKey, t, locale]);

  async function remove(row: AdminFavoriteRow) {
    if (!allowed || deleting) return;
    setDeleting(true);
    setActionError("");
    setDeleted(false);
    try {
      await deleteAdminFavorite(row);
      setPending(null);
      setDeleted(true);
      setNonce((value) => value + 1);
    } catch (err) {
      setPending(null);
      setActionError(describeError(toErrorLike(err), t, locale, COMMUNITY_REPORT_REVIEW));
    } finally { setDeleting(false); }
  }

  if (!allowed) return <Card title={t("admin.favorites.title")}>
    <Notice kind="err" text={t("admin.denied.body", { codes: COMMUNITY_REPORT_REVIEW })} />
  </Card>;

  return <Card title={t("admin.favorites.title")} desc={t("admin.favorites.desc")} actions={<Button type="button" disabled={loading || deleting} onClick={() => setNonce((value) => value + 1)}>{t("admin.refresh")}</Button>}>
    <div className="space-y-3">
      <form className="flex flex-wrap items-end gap-3" onSubmit={(event) => { event.preventDefault(); setFilters({ ...draft }); setPage(1); }}>
        <div className="min-w-[14rem] flex-1"><Field label={t("admin.favorites.owner")}><TextInput value={draft.ownerID} placeholder={t("admin.favorites.idPlaceholder")} onChange={(event) => setDraft((value) => ({ ...value, ownerID: event.target.value }))} /></Field></div>
        <Field label={t("admin.favorites.targetType")}>
          <Select value={draft.targetType} onChange={(event) => setDraft((value) => ({ ...value, targetType: event.target.value as FavoriteTargetType | "" }))}>
            <option value="">{t("admin.favorites.allTypes")}</option>
            {TARGET_TYPES.map((type) => <option key={type} value={type}>{t(`admin.favorites.type.${type}`)}</option>)}
          </Select>
        </Field>
        <div className="min-w-[14rem] flex-1"><Field label={t("admin.favorites.target")}><TextInput value={draft.targetID} placeholder={t("admin.favorites.idPlaceholder")} onChange={(event) => setDraft((value) => ({ ...value, targetID: event.target.value }))} /></Field></div>
        <Button type="submit" variant="primary">{t("admin.search")}</Button>
        <Button type="button" onClick={() => { setDraft(EMPTY_FILTERS); setFilters(EMPTY_FILTERS); setPage(1); }}>{t("admin.clear")}</Button>
      </form>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <span className="text-xs text-muted">{t("admin.total", { total: formatNumber(total, locale) })}</span>
        <Field label={t("admin.favorites.pageSize")}><Select value={pageSize} onChange={(event) => { setPageSize(Number(event.target.value)); setPage(1); }}>{[20, 50, 100].map((value) => <option key={value} value={value}>{formatNumber(value, locale)}</option>)}</Select></Field>
      </div>
      {deleted ? <Notice kind="ok" text={t("admin.favorites.deleted")} onClose={() => setDeleted(false)} /> : null}
      {actionError ? <Notice kind="err" text={actionError} onClose={() => setActionError("")} /> : null}
      {error ? <Notice kind="err" text={error} /> : null}
      <div className="overflow-x-auto rounded-lg border border-line">
        <table className="w-full min-w-[40rem] border-collapse"><thead className="bg-surface"><tr>
          {["owner", "targetType", "target", "createdAt"].map((key) => <th key={key} scope="col" className={HEAD_CLASS}>{t(`admin.favorites.${key}`)}</th>)}
          <th scope="col" className={HEAD_CLASS}>{t("admin.actions")}</th>
        </tr></thead><tbody className="divide-y divide-line">
          {loading ? <LoadingRow colSpan={5} text={t("admin.loading")} /> : error ? null : rows.length === 0 ? <EmptyRow colSpan={5} text={t("admin.favorites.empty")} /> : rows.map((row) => <tr key={rowKey(row)}>
            <td className={CELL_CLASS + " break-all font-mono"}>{row.owner_id}</td>
            <td className={CELL_CLASS}>{t(`admin.favorites.type.${row.target_type}`)}</td>
            <td className={CELL_CLASS + " break-all font-mono"}><a href={"/catalog/" + encodeURIComponent(row.target_id)} target="_blank" rel="noreferrer" className="text-ink hover:underline">{row.target_id}</a></td>
            <td className={CELL_CLASS}>{formatDateTime(row.created_at, locale)}</td>
            <td className={CELL_CLASS}><Button type="button" variant="danger" disabled={deleting} onClick={() => setPending(row)}>{t("admin.delete")}</Button></td>
          </tr>)}
        </tbody></table>
      </div>
      <Pagination page={page} pages={pages} onChange={setPage} disabled={loading || !!error || deleting} labels={{ prev: t("admin.prev"), next: t("admin.next"), info: t("admin.pageInfo", { page, pages }) }} />
    </div>
    {pending ? <ConfirmDialog title={t("admin.favorites.deleteTitle")} body={t("admin.favorites.deleteBody", { owner: pending.owner_id, type: t(`admin.favorites.type.${pending.target_type}`), target: pending.target_id })} confirmLabel={t("admin.delete")} cancelLabel={t("admin.cancel")} danger busy={deleting} onConfirm={() => remove(pending)} onClose={() => setPending(null)} /> : null}
  </Card>;
}
