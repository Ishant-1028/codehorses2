import Card from "./Card";
import Button from "./Button";

export default function HeroCard() {
  return (
    <Card className="flex flex-col gap-3 bg-gradient-to-r from-blue-600 to-purple-600">
      <h2 className="text-xl font-bold">🔥 Next Contest</h2>

      <p>CF Round #2224</p>
      <p>Starts in: 2h 15m</p>
      <p>Prize Pool: 2500 coins</p>

      <Button>Join Now</Button>
    </Card>
  );
}