type CardProps = {
  children: React.ReactNode;
  className?: string;
};

export default function Card({ children, className }: CardProps) {
  return (
    <div
      className={`bg-slate-800 rounded-2xl p-4 shadow-lg border border-slate-700 ${className}`}
    >
      {children}
    </div>
  );
}