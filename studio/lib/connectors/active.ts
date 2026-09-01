import type {
  ComposioAliasMap,
  ComposioConnectedAccount,
  MCPStatus,
} from "@/lib/api";

/**
 * What "Accounts" contains, defined ONCE.
 *
 * The Settings rail used to count `servers.length` — the MCP processes that
 * answered `/api/mcp` — while the Accounts screen itself listed those servers
 * PLUS every connected Composio account. Two owners of one number, so they
 * disagreed the moment a sixth mailbox was connected: the rail said 2, the
 * screen listed nine rows.
 *
 * Grouping now lives here as a pure function. The rail counts the groups it
 * builds and the screen renders them, so the number and the list cannot drift
 * apart again.
 */

export type ActiveAccount = {
  id: string;
  accountId?: string; // composio-only
  ok: boolean;
  error?: string;
  statusText: string;
  alias?: string;
  identityHint?: string;
  userId?: string;
  createdAt?: string;
  tools: string[];
};

export type ActiveGroup = {
  kind: "native" | "composio";
  key: string;
  slug: string;
  name: string;
  source: string;
  logo?: string;
  accounts: ActiveAccount[];
};

/**
 * Native MCP servers from mcp.yaml render as single-account groups; Composio
 * accounts collapse into one group per toolkit with a sub-row each. The
 * composio parent MCP entry is hidden once any toolkit account is connected —
 * its connection status is implicit in the children.
 */
export function buildActiveConnectorGroups(
  servers: MCPStatus[],
  connected: ComposioConnectedAccount[],
  aliases: ComposioAliasMap,
): ActiveGroup[] {
  const groups: ActiveGroup[] = [];
  const hasComposio = connected.length > 0;

  for (const s of servers) {
    if (hasComposio && s.name === "composio") continue;
    groups.push({
      kind: "native",
      key: `mcp:${s.name}`,
      slug: s.name,
      name: s.name,
      source: nativeSourceLabel(s.name),
      accounts: [
        {
          id: s.name,
          ok: s.connected,
          error: s.error,
          tools: s.tools ?? [],
          statusText: s.connected ? "ACTIVE" : s.error ? "ERROR" : "PENDING",
        },
      ],
    });
  }

  const byToolkit = new Map<string, ComposioConnectedAccount[]>();
  for (const acc of connected) {
    const slug = (acc.toolkit?.slug ?? "unknown").toLowerCase();
    const arr = byToolkit.get(slug) ?? [];
    arr.push(acc);
    byToolkit.set(slug, arr);
  }
  for (const [slug, accs] of byToolkit.entries()) {
    const first = accs[0];
    groups.push({
      kind: "composio",
      key: `composio:${slug}`,
      slug,
      name: first.toolkit?.name ?? slug,
      source: "via Composio",
      logo: first.toolkit?.logo,
      accounts: visibleConnectorAccounts(accs, aliases).map((a) => ({
        id: a.id,
        accountId: a.id,
        ok: ((a.status ?? "").toUpperCase() || "ACTIVE") === "ACTIVE",
        statusText: (a.status ?? "ACTIVE").toUpperCase(),
        alias: aliases[a.id] ?? "",
        identityHint: extractIdentityHint(a),
        userId: a.user_id,
        createdAt: a.created_at,
        tools: [],
      })),
    });
  }

  return groups;
}

/** Search filter for the Active list. Never applied to the rail count. */
export function filterActiveConnectorGroups(
  groups: ActiveGroup[],
  query: string,
): ActiveGroup[] {
  const q = query.trim().toLowerCase();
  if (!q) return groups;
  return groups.filter((g) => {
    if (g.name.toLowerCase().includes(q) || g.slug.toLowerCase().includes(q)) return true;
    for (const a of g.accounts) {
      if (a.alias?.toLowerCase().includes(q)) return true;
      if (a.identityHint?.toLowerCase().includes(q)) return true;
      if (a.tools?.some((t) => t.toLowerCase().includes(q))) return true;
    }
    return false;
  });
}

/** One number: how many account rows the Accounts screen has to show. */
export function countActiveConnectorAccounts(groups: ActiveGroup[]): number {
  return groups.reduce((sum, g) => sum + g.accounts.length, 0);
}

export function isReconnectableAccount(account: ActiveAccount) {
  if (account.ok) return false;
  return isBadConnectorStatus(account.statusText.toUpperCase());
}

export function visibleConnectorAccounts(
  accounts: ComposioConnectedAccount[],
  aliases: ComposioAliasMap,
) {
  const byIdentity = new Map<string, ComposioConnectedAccount>();
  for (const account of accounts) {
    const key = connectorIdentityKey(account, aliases);
    const existing = byIdentity.get(key);
    if (!existing || connectorAccountDisplayRank(account) > connectorAccountDisplayRank(existing)) {
      byIdentity.set(key, account);
    }
  }
  return Array.from(byIdentity.values()).sort((a, b) => {
    const rank = connectorAccountDisplayRank(b) - connectorAccountDisplayRank(a);
    if (rank !== 0) return rank;
    return Date.parse(a.created_at ?? "") - Date.parse(b.created_at ?? "");
  });
}

function connectorIdentityKey(account: ComposioConnectedAccount, aliases: ComposioAliasMap) {
  const slug = (account.toolkit?.slug ?? "unknown").toLowerCase();
  const identity = (aliases[account.id] || extractIdentityHint(account) || account.user_id || "")
    .trim()
    .toLowerCase();
  return identity ? `${slug}:${identity}` : `${slug}:${account.id}`;
}

function connectorAccountDisplayRank(account: ComposioConnectedAccount) {
  const status = (account.status ?? "ACTIVE").toUpperCase();
  const statusRank =
    status === "ACTIVE" ? 500 :
      status === "INITIATED" || status === "INITIALIZING" || status === "PENDING" ? 400 :
        isBadConnectorStatus(status) ? 100 :
          300;
  const created = Date.parse(account.created_at ?? "");
  return statusRank + (Number.isFinite(created) ? created / 1_000_000_000_000_000 : 0);
}

export function isBadConnectorStatus(status: string) {
  return (
    status === "REVOKED" ||
    status === "EXPIRED" ||
    status === "INACTIVE" ||
    status === "DISABLED" ||
    status === "ERROR" ||
    status.includes("FAILED") ||
    status.includes("UNAUTHORIZED") ||
    status.includes("EXPIRED") ||
    status.includes("REVOKED")
  );
}

export function isPendingConnectorStatus(status: string) {
  const s = status.toUpperCase();
  return s === "INITIATED" || s === "INITIALIZING" || s === "PENDING";
}

// extractIdentityHint pulls a human-recognisable identity out of Composio's
// account meta/data blobs. The exact field name varies per toolkit
// (email for Gmail, login for GitHub, team_name for Slack) so we walk
// a list of common identity keys. Best-effort - returns "" when the
// upstream response doesn't surface anything usable.
export function extractIdentityHint(acc: ComposioConnectedAccount): string {
  const candidates = ["email", "username", "user_email", "login", "display_name", "name", "team_name", "workspace_name"];
  const pools: Array<Record<string, unknown> | undefined> = [acc.meta, acc.data];
  for (const pool of pools) {
    if (!pool) continue;
    for (const key of candidates) {
      const v = pool[key];
      if (typeof v === "string" && v.trim() !== "") return v.trim();
    }
    for (const nestedKey of ["user", "profile", "account", "authed_user"]) {
      const nested = pool[nestedKey];
      if (nested && typeof nested === "object") {
        for (const key of candidates) {
          const v = (nested as Record<string, unknown>)[key];
          if (typeof v === "string" && v.trim() !== "") return v.trim();
        }
      }
    }
  }
  return "";
}

export function nativeSourceLabel(name: string): string {
  switch (name) {
    case "claude_code":
      return "Mac bridge";
    case "github":
      return "Direct API";
    case "composio":
      return "Gateway";
    default:
      return "Direct MCP";
  }
}
