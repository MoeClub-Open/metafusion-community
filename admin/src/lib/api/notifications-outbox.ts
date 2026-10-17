"use client";

import { fetchApi } from "./client";

/** Mirrors internal/store/notification_outbox.go. Terminal states are not replayable. */
export type NotificationOutboxStatus = "pending" | "sent" | "failed" | "expired";

export interface NotificationOutboxItem {
  id: string;
  recipient_id: string;
  type: string;
  subject_type: string;
  subject_id: string;
  dedupe_key: string;
  event_id: string;
  actor_id: string;
  actor_name: string;
  payload: Record<string, unknown>;
  status: NotificationOutboxStatus;
  attempts: number;
  next_retry_at: string;
  expires_at: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface NotificationOutboxPage {
  items: NotificationOutboxItem[];
  total: number;
}

export interface NotificationOutboxRetryResult {
  ok: boolean;
  retried: number;
  sent: number;
  expired: number;
}

/** Both endpoints require community.report.review, enforced independently by the service. */
export function fetchNotificationsOutbox(params: {
  status: NotificationOutboxStatus | "";
  page: number;
  pageSize: number;
}): Promise<NotificationOutboxPage> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.status) query.set("status", params.status);
  return fetchApi<NotificationOutboxPage>("/api/community/admin/notifications/outbox?" + query);
}

/** Claims at most 50 due pending rows; it does not reset failed/expired rows. */
export function retryDueNotificationsOutbox(): Promise<NotificationOutboxRetryResult> {
  return fetchApi<NotificationOutboxRetryResult>("/api/community/admin/notifications/outbox/retry", { method: "POST" });
}
