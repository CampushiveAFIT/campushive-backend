-- DropIndex
DROP INDEX "Ticket_ticketCodeHash_key";

-- AlterTable
ALTER TABLE "Ticket" DROP COLUMN "ticketCodeHash",
ADD COLUMN     "ticketCode" TEXT NOT NULL;

-- CreateIndex
CREATE UNIQUE INDEX "Ticket_ticketCode_key" ON "Ticket"("ticketCode");

