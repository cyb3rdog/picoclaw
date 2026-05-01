import { createFileRoute } from "@tanstack/react-router"

import { SWLPage } from "@/components/swl/swl-page"

export const Route = createFileRoute("/swl")({
  component: SWLPage,
})
