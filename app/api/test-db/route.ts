// /app/api/test-db/route.ts
// Deprecated test route - schema has migrated to Lineup model

export async function GET() {
  return Response.json({
    success: false,
    error: "This test route is deprecated. Use /api/lineup/create instead.",
  });
}