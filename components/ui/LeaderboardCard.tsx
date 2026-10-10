import Card from "./Card";

type User = {
  rank: number;
  email: string;
  coins: number;
};

export default function LeaderboardCard({ users }: { users: User[] }) {
  return (
    <Card>
      <h2 className="text-lg font-bold mb-3">Leaderboard</h2>

      <div className="flex flex-col gap-2">
        {users.map((u) => (
          <div
            key={u.rank}
            className="flex justify-between text-sm bg-slate-700 p-2 rounded"
          >
            <span>#{u.rank}</span>
            <span>{u.email}</span>
            <span>{u.coins}</span>
          </div>
        ))}
      </div>
    </Card>
  );
}