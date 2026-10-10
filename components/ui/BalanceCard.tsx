import Card from "./Card";

export default function BalanceCard({
  totalCents,
}: {
  totalCents: number;
}) {
  return (
    <Card className="flex justify-between items-center">
      <span className="text-gray-400">Balance</span>
      <span className="text-xl font-bold">{totalCents} PLAY_COIN</span>
    </Card>
  );
}