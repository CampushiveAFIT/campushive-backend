DROP INDEX IF EXISTS "Ticket_ticketCode_key";
ALTER TABLE "Ticket" DROP COLUMN "ticketCode", ADD COLUMN "ticketCodeHash" TEXT NOT NULL;
CREATE UNIQUE INDEX "Ticket_ticketCodeHash_key" ON "Ticket"("ticketCodeHash");
