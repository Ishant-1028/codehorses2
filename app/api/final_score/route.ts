// Deprecated: This route used old 5-handle scoring logic.
// New scoring is handled by packages/scoring-rules/rules.go

export async function POST(req: Request) {
  return Response.json({
    error: "This endpoint is deprecated. Scoring is now handled by the Go scoring service.",
  }, { status: 410 });
}