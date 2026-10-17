"use client";

import { fetchApi } from "./client";

export type FavoriteTargetType = "agent" | "collection" | "work" | "content_unit" | "expression" | "release" | "medium" | "track";

/** Raw community-owned record; no catalog lookup or visibility projection. */
export interface AdminFavoriteRow {
  owner_id: string;
  target_type: FavoriteTargetType;
  target_id: string;
  created_at: string;
}

export interface AdminFavoritesPage {
  items: AdminFavoriteRow[];
  total: number;
}

export function fetchAdminFavorites(params: {
  ownerID: string;
  targetType: FavoriteTargetType | "";
  targetID: string;
  page: number;
  pageSize: number;
}): Promise<AdminFavoritesPage> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.ownerID.trim()) query.set("owner_id", params.ownerID.trim());
  if (params.targetType) query.set("target_type", params.targetType);
  if (params.targetID.trim()) query.set("target_id", params.targetID.trim());
  return fetchApi<AdminFavoritesPage>("/api/community/admin/favorites?" + query);
}

/** Deletes the exact owner/type/target tuple, independently authorized and audited by the service. */
export function deleteAdminFavorite(row: AdminFavoriteRow): Promise<void> {
  const query = new URLSearchParams({ owner_id: row.owner_id, target_type: row.target_type, target_id: row.target_id });
  return fetchApi<void>("/api/community/admin/favorites?" + query, { method: "DELETE" });
}
