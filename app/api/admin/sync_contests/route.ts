import prisma from "@/lib/db";

export async function GET() {
  try {
    const res = await fetch("https://codeforces.com/api/contest.list");
    const data = await res.json();

    if (data.status !== "OK") {
      return Response.json({ error: "CF API failed" }, { status: 500 });
    }

    const contests = data.result;

    let added = 0;

    for (const c of contests) {
      // ✅ Filter only upcoming CF contests
      if (c.type !== "CF" || c.phase !== "BEFORE") continue;
      if (!c.startTimeSeconds) continue;

      // 🔹 Compute lockTime (2 min before start)
      const lockTime = new Date((c.startTimeSeconds - 120) * 1000);

      // 🔹 Check if already exists
      const exists = await prisma.contest.findFirst({
        where: { contestId: c.id },
      });

      if (exists) continue;

      // 🔹 Create contest
      await prisma.contest.create({
        data: {
          title: c.name,
          contestId: c.id,
          entryFee: 50, // default (you can change later)
          lockTime,
        },
      });

      added++;
    }

    return Response.json({
      success: true,
      added,
    });

  } catch (err) {
    return Response.json(
      { error: "Failed to sync contests" },
      { status: 500 }
    );
  }
}