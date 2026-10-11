import { redirect } from 'next/navigation';

/** A deep link to the panel "Give access": the list opens behind it. */
export default function Page() {
  redirect('/access?grant=1');
}
