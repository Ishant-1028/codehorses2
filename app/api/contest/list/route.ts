import prisma from "@/lib/db";

export async function GET() {
  try {
    const contests = await prisma.contest.findMany({
      orderBy: {
        startAt: "asc",
      },
      select: {
        id: true,
        cfContestId: true,
        title: true,
        division: true,
        entryFeeCents: true,
        guaranteedPoolCents: true,
        prizeStructure: true,
        status: true,
        startAt: true,
        lockAt: true,
        durationSecs: true,
        realMoneyEnabled: true,
        createdAt: true,
      },
      take: 20,
    });

    return Response.json({ contests });
  } catch (err) {
    console.error("Contest list error:", err);
    return Response.json(
      { error: "Failed to fetch contests" },
      { status: 500 }
    );
  }
}