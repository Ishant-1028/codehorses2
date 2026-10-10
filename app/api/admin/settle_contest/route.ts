export const dynamic = "force-dynamic";
import prisma from "@/lib/db";
import { Bet } from "@prisma/client";

// One call to Codeforces `contest.standings` with the union of every
// handle across every bet is a fixed cost regardless of user count.
// The old implementation did `fetch(localhost/api/cf_score)` per handle
// per bet -- 5*N fetches that self-DoS'd the Next.js runtime and got
// throttled by Codeforces at any real scale.
async function fetchRanks(
  cfContestId: number,
  handles: string[]
): Promise<Map<string, number>> {
  const ranks = new Map<string, number>();
  if (handles.length === 0) return ranks;

  // CF caps handles at 10000 per call, but sends 400 for too-long URLs.
  // Chunk conservatively at 500 handles/request.
  const CHUNK = 500;
  for (let i = 0; i < handles.length; i += CHUNK) {
    const chunk = handles.slice(i, i + CHUNK);
    const url =
      `https://codeforces.com/api/contest.standings` +
      `?contestId=${cfContestId}` +
      `&handles=${chunk.join(";")}` +
      `&showUnofficial=false`;

    const res = await fetch(url, {
      // Never let Next cache admin settlement calls.
      cache: "no-store",
    });

    if (!res.ok) {
      throw new Error(`CF standings failed: ${res.status}`);
    }

    const data = await res.json();
    if (data.status !== "OK") {
      throw new Error(`CF standings status: ${data.status} ${data.comment ?? ""}`);
    }

    for (const row of data.result.rows ?? []) {
      const members = row.party?.members ?? [];
      for (const m of members) {
        if (m.handle) {
          ranks.set(m.handle, row.rank);
        }
      }
    }
  }

  // Handles that never appear in the standings (didn't register /
  // didn't submit) get a sentinel worst rank rather than a hard failure.
  const WORST = 100000;
  for (const h of handles) {
    if (!ranks.has(h)) ranks.set(h, WORST);
  }
  return ranks;
}

function getReward(rank: number, pool: number) {
  if (rank === 1) return Math.floor(pool * 0.4);
  if (rank === 2) return Math.floor(pool * 0.25);
  if (rank === 3) return Math.floor(pool * 0.15);
  if (rank <= 10) return Math.floor((pool * 0.2) / 7);
  return 0;
}

type ScoredBet = {
  bet: Bet;
  score: number;
};

export async function POST(req: Request) {
  try {
    const { contestId } = await req.json();

    if (!contestId) {
      return Response.json({ error: "Missing contestId" }, { status: 400 });
    }

    const contest = await prisma.contest.findUnique({
      where: { id: contestId },
      include: { bets: true },
    });

    if (!contest) {
      return Response.json({ error: "Contest not found" }, { status: 404 });
    }

    if (contest.status === "SETTLED") {
      return Response.json({ error: "Already settled" }, { status: 400 });
    }

    if (contest.status !== "LOCKED" && contest.status !== "PROCESSING") {
      return Response.json(
        { error: "Contest not ready for settlement" },
        { status: 400 }
      );
    }

    const bets = contest.bets;
    if (bets.length === 0) {
      return Response.json({ error: "No bets found" }, { status: 400 });
    }

    // Union of handles across every bet -- ranks are looked up once,
    // reused for every bet that names the same handle.
    const uniqueHandles = Array.from(
      new Set(bets.flatMap((b) => b.handles as string[]))
    );

    const ranks = await fetchRanks(contest.contestId, uniqueHandles);

    const multipliers = [2, 1.5, 1.1, 1, 0.9];
    const scoredBets: ScoredBet[] = bets.map((bet) => {
      let totalScore = 0;
      const handles = bet.handles as string[];
      for (let i = 0; i < handles.length && i < multipliers.length; i++) {
        const rank = ranks.get(handles[i]) ?? 100000;
        const score = rank === 0 ? 0 : 10000 / rank;
        totalScore += score * multipliers[i];
      }
      return { bet, score: totalScore };
    });

    scoredBets.sort((a, b) => b.score - a.score);

    const pool = contest.entryFee * bets.length;

    await prisma.$transaction(async (tx) => {
      for (let i = 0; i < scoredBets.length; i++) {
        const { bet, score } = scoredBets[i];
        const rank = i + 1;
        const reward = getReward(rank, pool);

        await tx.bet.update({
          where: { id: bet.id },
          data: { score, rank },
        });

        if (reward > 0) {
          await tx.user.update({
            where: { id: bet.userId },
            data: { coins: { increment: reward } },
          });

          await tx.transaction.create({
            data: {
              userId: bet.userId,
              amount: reward,
              type: "WIN",
            },
          });
        }
      }

      await tx.contest.update({
        where: { id: contestId },
        data: { status: "SETTLED" },
      });
    });

    return Response.json({
      success: true,
      message: "Contest settled",
      betsSettled: scoredBets.length,
      cfApiCalls: Math.ceil(uniqueHandles.length / 500),
    });
  } catch (err: any) {
    return Response.json(
      { error: err.message || "Settlement failed" },
      { status: 500 }
    );
  }
}
