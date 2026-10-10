export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const handle = searchParams.get("handle");
  const contestId = searchParams.get("contestId");

  if (!handle || !contestId) {
    return Response.json({ rank: 0 });
  }

  try {
    const res = await fetch(
      `https://codeforces.com/api/user.rating?handle=${handle}`
    );

    const data = await res.json();

    if (data.status !== "OK") {
      return Response.json({ rank: 0 });
    }

    const contest = data.result.find(
      (c: any) => c.contestId === Number(contestId)
    );

    if (!contest) {
      return Response.json({ rank: 0 });
    }

    return Response.json({ rank: contest.rank });
  } catch (err) {
    return Response.json({ rank: 0 });
  }
}