import jwt from "jsonwebtoken";

const SECRET = process.env.JWT_SECRET!;

export function signToken(user: { id: string; email: string }) {
  return jwt.sign(user, SECRET, { expiresIn: "7d" });
}

export function verifyToken(token: string) {
  return jwt.verify(token, SECRET);
}