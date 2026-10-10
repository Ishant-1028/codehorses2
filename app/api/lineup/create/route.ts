import prisma from "@/lib/db";
import { getServerSession } from "next-auth";
import { authOptions } from "@/lib/auth";
import { validateLineup, RULES_VERSION, TEAM_SIZE } from "@/lib/scoring";

type CreateLineupRequest = {
  contestId: string;
  handles: string[];
  captain: string;
  viceCaptain: string;
  salaries: number[];
  idempotencyKey: string;
};

export async function POST(req: Request) {
  try {
    const body: CreateLineupRequest = await req.json();
    const { contestId, handles, captain, viceCaptain, salaries, idempotencyKey } = body;

    // Require idempotency key
    if (!idempotencyKey) {
      return Response.json(
        { error: "idempotencyKey is required" },
        { status: 400 }
      );
    }

    // Auth check
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

    // Validate required fields
    if (!contestId || !handles || !captain || !viceCaptain || !salaries) {
      return Response.json({ error: "Missing required fields" }, { status: 400 });
    }

    // Check deduplication first (idempotency)
    const existingLineup = await prisma.lineup.findUnique({
      where: { entryTxnId: idempotencyKey },
    });

    if (existingLineup) {
      return Response.json({
        success: true,
        lineupId: existingLineup.id,
        txnRef: existingLineup.entryTxnId,
        deduped: true,
      });
    }

    // Normalize handles
    const cleanHandles = handles.map(h => h.trim());

    // Validate lineup
    const validation = validateLineup(cleanHandles, captain, viceCaptain, salaries);
    if (!validation.ok) {
      return Response.json({ error: validation.error }, { status: 400 });
    }

    // Transaction: contest checks + ledger + balance + lineup
    const result = await prisma.$transaction(async (tx) => {
      // Check contest exists and is joinable
      const contest = await tx.contest.findUnique({
        where: { id: contestId },
      });

      if (!contest) {
        throw new Error("Contest not found");
      }

      if (contest.status !== "UPCOMING") {
        throw new Error("Contest is not open for entry");
      }

      if (new Date() >= contest.lockAt) {
        throw new Error("Contest entry period has ended");
      }

      // Check if user already joined
      const existingEntry = await tx.lineup.findUnique({
        where: {
          userId_contestId: {
            userId: user.id,
            contestId: contest.id,
          },
        },
      });

      if (existingEntry) {
        throw new Error("Already joined this contest");
      }

      const entryFeeCents = Number(contest.entryFeeCents);

      // Check user has enough balance (priority: bonus > deposit > winning)
      const balances = await tx.balance.findMany({
        where: {
          accountId: user.id,
          bucket: { in: ["bonus", "deposit", "winning"] },
        },
      });

      const balanceMap = new Map<string, bigint>();
      balances.forEach(b => balanceMap.set(b.bucket, b.amountCents));

      const bonusCents = Number(balanceMap.get("bonus") || 0n);
      const depositCents = Number(balanceMap.get("deposit") || 0n);
      const winningCents = Number(balanceMap.get("winning") || 0n);

      const totalCents = bonusCents + depositCents + winningCents;

      if (totalCents < entryFeeCents) {
        throw new Error(`Insufficient balance. Need ${entryFeeCents} cents, have ${totalCents} cents`);
      }

      // Compute spend amounts from each bucket (priority order)
      let remaining = entryFeeCents;
      const spendBonus = Math.min(bonusCents, remaining);
      remaining -= spendBonus;

      const spendDeposit = Math.min(depositCents, remaining);
      remaining -= spendDeposit;

      const spendWinning = Math.min(winningCents, remaining);
      remaining -= spendWinning;

      if (remaining > 0) {
        throw new Error("Balance calculation error");
      }

      // Create ledger entries (double-entry, must sum to zero)
      const txnId = crypto.randomUUID();
      const ledgerEntries = [];

      if (spendBonus > 0) {
        ledgerEntries.push({
          txnId,
          accountId: user.id,
          bucket: "bonus" as const,
          amountCents: BigInt(-spendBonus),
          reason: "entry_fee" as const,
          refType: "Lineup",
          refId: null, // Will be set after lineup creation
          idempotencyKey: `${idempotencyKey}-bonus-debit`,
          createdAt: new Date(),
        });
      }

      if (spendDeposit > 0) {
        ledgerEntries.push({
          txnId,
          accountId: user.id,
          bucket: "deposit" as const,
          amountCents: BigInt(-spendDeposit),
          reason: "entry_fee" as const,
          refType: "Lineup",
          refId: null,
          idempotencyKey: `${idempotencyKey}-deposit-debit`,
          createdAt: new Date(),
        });
      }

      if (spendWinning > 0) {
        ledgerEntries.push({
          txnId,
          accountId: user.id,
          bucket: "winning" as const,
          amountCents: BigInt(-spendWinning),
          reason: "entry_fee" as const,
          refType: "Lineup",
          refId: null,
          idempotencyKey: `${idempotencyKey}-winning-debit`,
          createdAt: new Date(),
        });
      }

      // Balancing credit to house
      ledgerEntries.push({
        txnId,
        accountId: "house",
        bucket: "house" as const,
        amountCents: BigInt(entryFeeCents),
        reason: "entry_fee" as const,
        refType: "Lineup",
        refId: null,
        idempotencyKey: `${idempotencyKey}-house-credit`,
        createdAt: new Date(),
      });

      // Insert ledger entries
      for (const entry of ledgerEntries) {
        await tx.ledgerEntry.create({ data: entry });
      }

      // Update Balance cache
      if (spendBonus > 0) {
        await tx.balance.upsert({
          where: {
            accountId_bucket: {
              accountId: user.id,
              bucket: "bonus",
            },
          },
          update: {
            amountCents: { decrement: BigInt(spendBonus) },
          },
          create: {
            accountId: user.id,
            bucket: "bonus",
            amountCents: BigInt(-spendBonus),
          },
        });
      }

      if (spendDeposit > 0) {
        await tx.balance.upsert({
          where: {
            accountId_bucket: {
              accountId: user.id,
              bucket: "deposit",
            },
          },
          update: {
            amountCents: { decrement: BigInt(spendDeposit) },
          },
          create: {
            accountId: user.id,
            bucket: "deposit",
            amountCents: BigInt(-spendDeposit),
          },
        });
      }

      if (spendWinning > 0) {
        await tx.balance.upsert({
          where: {
            accountId_bucket: {
              accountId: user.id,
              bucket: "winning",
            },
          },
          update: {
            amountCents: { decrement: BigInt(spendWinning) },
          },
          create: {
            accountId: user.id,
            bucket: "winning",
            amountCents: BigInt(-spendWinning),
          },
        });
      }

      // Create house balance entry
      await tx.balance.upsert({
        where: {
          accountId_bucket: {
            accountId: "house",
            bucket: "house",
          },
        },
        update: {
          amountCents: { increment: BigInt(entryFeeCents) },
        },
        create: {
          accountId: "house",
          bucket: "house",
          amountCents: BigInt(entryFeeCents),
        },
      });

      // Create the Lineup
      const totalSalary = salaries.reduce((sum, s) => sum + s, 0);

      const lineup = await tx.lineup.create({
        data: {
          userId: user.id,
          contestId: contest.id,
          entryFeeCents: BigInt(entryFeeCents),
          entryTxnId: idempotencyKey,
          captainHandle: captain.trim(),
          viceCaptainHandle: viceCaptain.trim(),
          handles: cleanHandles,
          salaryUsed: totalSalary,
          rulesVersion: RULES_VERSION,
        },
      });

      // Update ledger entries with the lineup id
      await tx.ledgerEntry.updateMany({
        where: { txnId },
        data: { refId: lineup.id },
      });

      return lineup;
    });

    return Response.json({
      success: true,
      lineupId: result.id,
      txnRef: result.entryTxnId,
    });

  } catch (err: any) {
    console.error("Lineup creation error:", err);
    return Response.json(
      { error: err.message || "Failed to create lineup" },
      { status: 400 }
    );
  }
}
