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

    const currentUser = await prisma.user.findUnique({
      where: { email: session.user.email },
    });

    if (!currentUser || currentUser.id !== userId) {
      return Response.json({ error: "Forbidden" }, { status: 403 });
    }

    // Try reading from Balance cache first
    const balances = await prisma.balance.findMany({
      where: {
        accountId: userId,
        bucket: { in: ["deposit", "winning", "bonus"] },
      },
    });

    let depositCents = 0;
    let winningCents = 0;
    let bonusCents = 0;

    if (balances.length > 0) {
      // Use cached values
      balances.forEach(b => {
        const amount = Number(b.amountCents);
        if (b.bucket === "deposit") depositCents = amount;
        if (b.bucket === "winning") winningCents = amount;
        if (b.bucket === "bonus") bonusCents = amount;
      });
    } else {
      // Fallback: compute from LedgerEntry
      const ledgerEntries = await prisma.ledgerEntry.groupBy({
        by: ["bucket"],
        where: {
          accountId: userId,
          bucket: { in: ["deposit", "winning", "bonus"] },
        },
        _sum: {
          amountCents: true,
        },
      });

      ledgerEntries.forEach(entry => {
        const amount = Number(entry._sum.amountCents || 0n);
        if (entry.bucket === "deposit") depositCents = amount;
        if (entry.bucket === "winning") winningCents = amount;
        if (entry.bucket === "bonus") bonusCents = amount;
      });
    }

    const totalCents = depositCents + winningCents + bonusCents;

    return Response.json({
      deposit_cents: depositCents,
      winning_cents: winningCents,
      bonus_cents: bonusCents,
      total_cents: totalCents,
    });
  } catch (err: any) {
    console.error("Fetch wallet error:", err);
    return Response.json(
      { error: err.message || "Failed to fetch wallet" },
      { status: 500 }
    );
  }
}
