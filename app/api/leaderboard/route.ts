import prisma from "@/lib/db";

export async function GET() {
  try {
    const users = await prisma.user.findMany({
      orderBy: {
        coins: "desc",
      },
      take: 50,
    });

    const leaderboard = users.map((user, index) => ({
      rank: index + 1,
      email: user.email,
      coins: user.coins,
    }));

    return Response.json(leaderboard);

  } catch (err) {
    return Response.json(
      { error: "Failed to fetch leaderboard" },
      { status: 500 }
    );
  }
}