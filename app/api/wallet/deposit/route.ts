import prisma from "@/lib/db";
import { getServerSession } from "next-auth";
import { authOptions } from "@/lib/auth";

type DepositRequest = {
  userId: string;
  cents: number;
  idempotencyKey: string;
};

export async function POST(req: Request) {
  try {
    const body: DepositRequest = await req.json();
    const { userId, cents, idempotencyKey } = body;

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

    const currentUser = await prisma.user.findUnique({
      where: { email: session.user.email },
    });

    if (!currentUser || currentUser.id !== userId) {
      return Response.json({ error: "Forbidden" }, { status: 403 });
    }

    if (!cents || cents <= 0) {
      return Response.json(
        { error: "Invalid deposit amount" },
        { status: 400 }
      );
    }

    // Check if idempotency key was already used
    const existingEntry = await prisma.ledgerEntry.findUnique({
      where: { idempotencyKey: `${idempotencyKey}-user-credit` },
    });

    if (existingEntry) {
      return Response.json({
        success: true,
        deduped: true,
        message: "Deposit already processed",
      });
    }

    // Create ledger entries (house debit, user credit)
    await prisma.$transaction(async (tx) => {
      const txnId = crypto.randomUUID();

      // House debit (negative)
      await tx.ledgerEntry.create({
        data: {
          txnId,
          accountId: "house",
          bucket: "house",
          amountCents: BigInt(-cents),
          reason: "deposit",
          refType: "Deposit",
          refId: userId,
          idempotencyKey: `${idempotencyKey}-house-debit`,
        },
      });

      // User credit (positive)
      await tx.ledgerEntry.create({
        data: {
          txnId,
          accountId: userId,
          bucket: "deposit",
          amountCents: BigInt(cents),
          reason: "deposit",
          refType: "Deposit",
          refId: userId,
          idempotencyKey: `${idempotencyKey}-user-credit`,
        },
      });

      // Update Balance cache - house
      await tx.balance.upsert({
        where: {
          accountId_bucket: {
            accountId: "house",
            bucket: "house",
          },
        },
        update: {
          amountCents: { decrement: BigInt(cents) },
        },
        create: {
          accountId: "house",
          bucket: "house",
          amountCents: BigInt(-cents),
        },
      });

      // Update Balance cache - user deposit
      await tx.balance.upsert({
        where: {
          accountId_bucket: {
            accountId: userId,
            bucket: "deposit",
          },
        },
        update: {
          amountCents: { increment: BigInt(cents) },
        },
        create: {
          accountId: userId,
          bucket: "deposit",
          amountCents: BigInt(cents),
        },
      });
    });

    return Response.json({
      success: true,
      deduped: false,
      message: "Deposit successful",
    });
  } catch (err: any) {
    console.error("Deposit error:", err);
    return Response.json(
      { error: err.message || "Deposit failed" },
      { status: 400 }
    );
  }
}
