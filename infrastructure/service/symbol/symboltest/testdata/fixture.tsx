import React from "react";

interface CardProps {
  title: string;
  body?: string;
}

export function Card({ title, body }: CardProps) {
  return (
    <section className="card">
      <h2>{title}</h2>
      <p>{body}</p>
    </section>
  );
}

export const Grid: React.FC<{ cols: number }> = ({ cols, children }) => (
  <div style={{ columns: cols }}>{children}</div>
);
