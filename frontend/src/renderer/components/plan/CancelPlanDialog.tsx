import { useTranslation } from "react-i18next";
import { usePlanMutations } from "../../hooks/usePlans";
import { apiErrorMessage } from "../../lib/api-client";
import { ConfirmDialog } from "../ConfirmDialog";

/** Confirms cancelling a Task. Its agents keep running; only scheduling stops. */
export function CancelPlanDialog({
	open,
	onOpenChange,
	planId,
	projectId,
	title,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	planId: string;
	projectId: string;
	title: string;
}) {
	const { t } = useTranslation();
	const { cancel } = usePlanMutations(planId, projectId);
	return (
		<ConfirmDialog
			open={open}
			onOpenChange={(next) => {
				if (!next) cancel.reset();
				onOpenChange(next);
			}}
			title={t("plan.cancel.title", { title })}
			description={t("plan.cancel.description")}
			confirmLabel={t("plan.cancel.confirm")}
			cancelLabel={t("plan.cancel.keep")}
			destructive
			busy={cancel.isPending}
			error={cancel.isError ? apiErrorMessage(cancel.error, t("plan.cancel.failed")) : null}
			onConfirm={() => cancel.mutate(undefined, { onSuccess: () => onOpenChange(false) })}
		/>
	);
}
