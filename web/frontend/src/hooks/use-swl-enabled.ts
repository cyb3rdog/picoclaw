import { useQuery } from "@tanstack/react-query"

import { launcherFetch } from "@/api/http"

function asRecord(v: unknown): Record<string, unknown> {
  return v && typeof v === "object" && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {}
}

interface SwlEnabledResult {
  enabled: boolean
  loading: boolean
}

export function useSwlEnabled(): SwlEnabledResult {
  const { data, isLoading } = useQuery<unknown>({
    queryKey: ["config"],
    queryFn: async () => {
      const res = await launcherFetch("/api/config")
      if (!res.ok) return null
      return res.json()
    },
    staleTime: 2 * 60 * 1000,
    retry: false,
  })

  const tools = asRecord(asRecord(data).tools)
  const swl = asRecord(tools.swl)
  return { enabled: swl.enabled === true, loading: isLoading }
}
