import prisma from "@/lib/db";
import { getServerSession } from "next-auth";
import { authOptions } from "@/lib/auth";
import { redirect } from "next/navigation";
import Link from "next/link";
import WalletCard from "@/components/ui/WalletCard";
import LogoutButton from "@/components/ui/LogoutButton";

export default async function Dashboard() {
  const session = await getServerSession(authOptions);

  if (!session?.user?.email) {
    redirect("/login");
  }

  const user = await prisma.user.findUnique({
    where: { email: session.user.email },
  });

  if (!user) {
    redirect("/login");
  }

  // Get contests
  const contests = await prisma.contest.findMany({
    orderBy: { startAt: "asc" },
  });

  // Get user's lineups
  const lineups = await prisma.lineup.findMany({
    where: { userId: user.id },
  });

  // O(1) lookup
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

  return (
    <div className="min-h-screen bg-black text-white p-6 flex justify-center">
      <div className="w-full max-w-5xl">
        {/* Header */}
        <div className="flex justify-between items-center mb-10">
          <h1 className="text-3xl font-bold bg-gradient-to-r from-purple-500 to-pink-500 bg-clip-text text-transparent">
            Dashboard
          </h1>
          <div className="flex items-center gap-4">
            <p className="text-gray-400 text-sm">{session.user.email}</p>
            <LogoutButton />
          </div>
        </div>

        {/* Wallet */}
        <div className="mb-10">
          <WalletCard userId={user.id} />
        </div>

        {/* UPCOMING */}
        <h2 className="text-xl font-semibold mb-4 text-purple-400">
          Upcoming Contests
        </h2>

        <div className="space-y-6 mb-10">
          {upcoming.length === 0 && (
            <p className="text-gray-500">No upcoming contests</p>
          )}

          {upcoming.map((contest: any) => (
            <div
              key={contest.id}
              className="bg-gray-900 border border-gray-800 rounded-2xl p-6 shadow-lg flex justify-between items-center"
            >
              <div>
                <p className="text-lg font-semibold">{contest.title}</p>
                <p className="text-gray-400 text-sm">
                  Entry Fee: {Number(contest.entryFeeCents)} PLAY_COIN
                </p>
              </div>

              <div>
                {contest.joined ? (
                  <span className="px-4 py-2 bg-green-700 rounded-xl text-sm">
                    Joined
                  </span>
                ) : (
                  <Link href={`/contest/${contest.id}`}>
                    <button className="bg-purple-600 hover:bg-purple-700 px-4 py-2 rounded-xl">
                      Join
                    </button>
                  </Link>
                )}
              </div>
            </div>
          ))}
        </div>

        {/* PAST */}
        <h2 className="text-xl font-semibold mb-4 text-gray-400">
          Past Contests
        </h2>

        <div className="space-y-6">
          {past.length === 0 && (
            <p className="text-gray-500">No past contests</p>
          )}

          {past.map((contest: any) => (
            <div
              key={contest.id}
              className="bg-gray-900 border border-gray-800 rounded-2xl p-6 shadow-lg flex justify-between items-center"
            >
              <div>
                <p className="text-lg font-semibold">{contest.title}</p>
                {contest.lineup && (
                  <p className="text-gray-400 text-sm">
                    Score: {contest.lineup.score?.toFixed(2) || "N/A"} | Rank:{" "}
                    {contest.lineup.rank || "N/A"}
                  </p>
                )}
              </div>

              <div>
                {contest.joined ? (
                  <span className="px-4 py-2 bg-green-700 rounded-xl text-sm">
                    Joined
                  </span>
                ) : (
                  <span className="px-4 py-2 bg-gray-800 rounded-xl text-sm">
                    Not Joined
                  </span>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}