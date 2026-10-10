import prisma from "@/lib/db";
import { getServerSession } from "next-auth";
import { authOptions } from "@/lib/auth";

export async function GET() {
  try {
    const session = await getServerSession(authOptions);

    if (!session?.user?.email) {
      return Response.json({ error: "Unauthorized" }, { status: 401 });
    }

    const user = await prisma.user.findUnique({
      where: { email: session.user.email },
    });

    if (!user) {
      return Response.json({ error: "User not found" }, { status: 404 });
    }

    const contests = await prisma.contest.findMany({
      orderBy: { startAt: "asc" },
    });

    const lineups = await prisma.lineup.findMany({
      where: { userId: user.id },
    });

    const lineupMap = new Map();
    lineups.forEach((l) => {
      lineupMap.set(l.contestId, l);
    });

    const upcoming: any[] = [];
    const past: any[] = [];

    for (const contest of contests) {
      const lineup = lineupMap.get(contest.id);

      const enriched = {
        ...contest,
        joined: !!lineup,
        lineup: lineup || null,
      };

      if (contest.status === "UPCOMING") {
        upcoming.push(enriched);
      } else {
        past.push(enriched);
      }
    }

    return Response.json({
      upcoming,
      past,
    });

  } catch (err: any) {
    return Response.json(
      { error: err.message || "Dashboard failed" },
      { status: 500 }
    );
  }
}