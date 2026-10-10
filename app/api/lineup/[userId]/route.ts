import prisma from "@/lib/db";
import { getServerSession } from "next-auth";
import { authOptions } from "@/lib/auth";

export async function GET(
  req: Request,
  context: { params: Promise<{ userId: string }> }
) {
  try {
    const session = await getServerSession(authOptions);
    if (!session?.user?.email) {
      return Response.json({ error: "Unauthorized" }, { status: 401 });
    }

    const { userId } = await context.params;

    // Optional: verify the requesting user matches userId or is admin
    const currentUser = await prisma.user.findUnique({
      where: { email: session.user.email },
    });

    if (!currentUser || currentUser.id !== userId) {
      return Response.json({ error: "Forbidden" }, { status: 403 });
    }

    // Fetch lineups with contest metadata
    const lineups = await prisma.lineup.findMany({
      where: { userId },
      include: {
        contest: {
          select: {
            id: true,
            cfContestId: true,
            title: true,
            division: true,
            entryFeeCents: true,
            status: true,
            startAt: true,
            lockAt: true,
          },
        },
      },
      orderBy: {
        createdAt: "desc",
      },
    });

    return Response.json({ lineups });
  } catch (err: any) {
    console.error("Fetch lineups error:", err);
    return Response.json(
      { error: err.message || "Failed to fetch lineups" },
      { status: 500 }
    );
  }
}
