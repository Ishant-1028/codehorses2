import prisma from "@/lib/db";
import { redirect } from "next/navigation";
import LineupBuilder from "@/components/ui/LineupBuilder";
import LiveLeaderboard from "@/components/ui/LiveLeaderboard";

export default async function Page({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;

  const contest = await prisma.contest.findUnique({
    where: { id },
  });

  if (!contest) {
    redirect("/dashboard");
  }

  const entryFeeCents = Number(contest.entryFeeCents);

  return (
    <div className="min-h-screen bg-black text-white p-6">
      <div className="max-w-6xl mx-auto">
        <div className="mb-8">
          <h1 className="text-3xl font-bold mb-2">{contest.title}</h1>
          <p className="text-gray-400">
            {contest.division && `Division: ${contest.division} | `}
            Status: {contest.status}
          </p>
        </div>

        <div className="grid lg:grid-cols-2 gap-8">
          <div>
            {contest.status === "UPCOMING" ? (
              <LineupBuilder
                contestId={contest.id}
                entryFeeCents={entryFeeCents}
              />
            ) : (
              <div className="bg-gray-900 border border-gray-800 rounded-2xl p-8 text-center">
                <p className="text-gray-400">Contest entry is closed</p>
              </div>
            )}
          </div>

          <div>
            <LiveLeaderboard cfContestId={contest.cfContestId} />
          </div>
        </div>
      </div>
    </div>
  );
}