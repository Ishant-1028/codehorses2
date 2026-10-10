import prisma from "@/lib/db";

export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const contestId = searchParams.get("contestId");

  if (!contestId) {
    return Response.json(
      { error: "Missing contestId" },
      { status: 400 }
    );
  }

  const lineups = await prisma.lineup.findMany({
    where: { contestId },
    include: {
      user: true,
    },
    orderBy: {
      rank: "asc",
    },
  });

  return Response.json(
    lineups.map((l) => ({
      rank: l.rank,
      score: l.score,
      user: l.user.email,
      handles: l.handles,
    }))
  );
}