import Card from "./Card";
import Button from "./Button";

type Props = {
  title: string;
  entryFee: number;
  pool: number;
  time: string;
};

export default function ContestCard({
  title,
  entryFee,
  pool,
  time,
}: Props) {
  return (
    <Card className="flex flex-col gap-3">
      <h3 className="text-lg font-bold">{title}</h3>

      <div className="text-sm text-gray-400">
        Starts in: {time}
      </div>

      <div className="flex justify-between text-sm">
        <span>Entry: {entryFee}</span>
        <span>Pool: {pool}</span>
      </div>

      <Button>Join</Button>
    </Card>
  );
}